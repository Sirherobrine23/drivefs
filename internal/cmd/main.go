package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"

	"golang.org/x/oauth2"
	"google.golang.org/api/drive/v3"
	"sirherobrine23.com.br/Sirherobrine23/drivefs"
)

var configPath string
var serverPort uint

func main() {
	flag.StringVar(&configPath, "config", "./config.json", "Config file path")
	flag.UintVar(&serverPort, "port", 8081, "server to listen")
	flag.Parse()

	var ggdrive *drivefs.Gdrive = new(drivefs.Gdrive)
	file, err := os.Open(configPath)
	if err != nil {
		panic(err)
	}
	defer file.Close()
	if err := json.NewDecoder(file).Decode(ggdrive); err != nil {
		panic(err)
	}

	if ggdrive.GoogleToken != nil {
		var err error
		if ggdrive, err = drivefs.New(ggdrive.GoogleOAuth, *ggdrive.GoogleToken); err != nil {
			panic(err)
		}
	} else {
		ln, err := net.Listen("tcp", ":0")
		if err != nil {
			panic(err)
		}
		P, _ := netip.ParseAddrPort(ln.Addr().String())
		ln.Close()
		ggdrive.GoogleOAuth.Redirects = []string{fmt.Sprintf("http://localhost:%d/callback", P.Port())}

		config := &oauth2.Config{ClientID: ggdrive.GoogleOAuth.Client, ClientSecret: ggdrive.GoogleOAuth.Secret, RedirectURL: ggdrive.GoogleOAuth.Redirects[0], Scopes: []string{drive.DriveScope, drive.DriveFileScope}, Endpoint: oauth2.Endpoint{AuthURL: ggdrive.GoogleOAuth.AuthURI, TokenURL: ggdrive.GoogleOAuth.TokenURI}}

		authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
		fmt.Printf("Go to the following link in your browser then type the authorization code: \n%v\n", authURL)

		var server *http.Server
		var code string
		mux := http.NewServeMux()
		mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte("Doned\n"))
			code = r.URL.Query().Get("code")
			if code != "" {
				fmt.Printf("Code: %q\n", code)
				w.Write([]byte(fmt.Sprintf("Code: %q\n", code)))
				w.Write(nil)
				server.Close()
			}
		})

		server = &http.Server{Addr: P.String(), Handler: mux}
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			panic(err)
		}

		ggdrive.GoogleToken, err = config.Exchange(context.TODO(), code)
		if err != nil {
			panic(fmt.Errorf("unable to retrieve token from web %v", err))
		}

		file.Close()
		if file, err = os.Create(configPath); err != nil {
			panic(err)
		}

		at := json.NewEncoder(file)
		at.SetIndent("", "  ")
		if err := at.Encode(ggdrive); err != nil {
			panic(err)
		} else if ggdrive, err = drivefs.New(ggdrive.GoogleOAuth, *ggdrive.GoogleToken); err != nil {
			panic(err)
		}
	}

	fmt.Printf("server listening on :%d\n", serverPort)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", serverPort), http.FileServerFS(ggdrive)); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}
