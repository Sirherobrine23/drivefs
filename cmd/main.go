package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"

	"golang.org/x/oauth2"
	"sirherobrine23.com.br/Sirherobrine23/cgofuse/fs"
	"sirherobrine23.com.br/Sirherobrine23/drivefs"
)

var (
	Config = flag.String("config", "", "config file")
	Target = flag.String("target", "", "target mount fs")
)

var _ fs.FileSystem[drivefs.File] = (*drivefs.Gdrive)(nil)

func main() {
	flag.Parse()
	configFile, err := os.OpenFile(*Config, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
		return
	}
	defer configFile.Close()
	runtime.KeepAlive(configFile)

	var gConfig drivefs.GoogleOauthConfig
	json.NewDecoder(configFile).Decode(&gConfig)

	gConfig.UserAuth = func(ctx context.Context, config *oauth2.Config) (token *oauth2.Token, err error) {
		listen, err := net.Listen("tcp", ":0")
		if err != nil {
			return nil, err
		}
		defer listen.Close()
		url, err := url.Parse("http://" + listen.Addr().String())
		if err != nil {
			return nil, err
		}
		_, port, err := net.SplitHostPort(url.Host)
		if err != nil {
			return nil, err
		}
		url.Host = "localhost:" + port
		config.RedirectURL = url.String()

		authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
		fmt.Fprintf(os.Stderr, "Go to the following link in your browser then type the authorization code:\n%v\n", authURL)

		http.Serve(listen, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			code := r.URL.Query().Get("code")
			if code == "" {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte("No code"))
				return
			}
			if token, err = config.Exchange(ctx, code); err == nil {
				gConfig.AccessToken = token.AccessToken
				gConfig.RefreshToken = token.RefreshToken
				gConfig.TokenType = token.TokenType
				gConfig.Expire = token.Expiry

				configFile.Seek(0, 0)
				js := json.NewEncoder(configFile)
				js.SetIndent("", "  ")
				if err = js.Encode(&gConfig); err != nil {
					fmt.Fprintf(os.Stderr, "Error: %s\n", err)
					os.Exit(1)
					return
				}
			}
			listen.Close()
		}))
		return
	}

	gdriveClient, err := drivefs.NewGoogleDrive(gConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
		return
	}
	defer func() {
		gConfig.AccessToken = gdriveClient.(*drivefs.Gdrive).GoogleToken.AccessToken
		gConfig.RefreshToken = gdriveClient.(*drivefs.Gdrive).GoogleToken.RefreshToken
		gConfig.TokenType = gdriveClient.(*drivefs.Gdrive).GoogleToken.TokenType
		gConfig.Expire = gdriveClient.(*drivefs.Gdrive).GoogleToken.Expiry
		configFile.Seek(0, 0)

		js := json.NewEncoder(configFile)
		js.SetIndent("", "  ")
		js.Encode(&gConfig)
	}()

	fmt.Fprintf(os.Stderr, "Mount overlayfs into %q\n", *Target)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, os.Kill)
	defer stop()

	cwd, err := filepath.Abs(*Target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
		return
	}

	if _, err := os.Stat(cwd); os.IsNotExist(err) {
		if err = os.Mkdir(cwd, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %s\n", err)
			os.Exit(1)
			return
		}
	}

	fs := fs.New(cwd, gdriveClient)
	if err := fs.Mount(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
		return
	}
	fmt.Fprintf(os.Stderr, "Mounted overlayfs in %q\n", cwd)

	<-ctx.Done()
	fmt.Fprintf(os.Stderr, "Unmount overlayfs\n")
	fs.Done()
	fmt.Fprintf(os.Stderr, "Unmounted overlayfs\n")
}
