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
	"google.golang.org/api/drive/v2"
	"sirherobrine23.com.br/Sirherobrine23/drivefs"
)

var (
	configPath = flag.String("config", "./config.json", "Config file path")
	serverPort = flag.Uint("port", 8081, "server to listen")
	setupAuth  = flag.Bool("auth", false, "Listen server and Auth")

	client        = flag.String("client", "", "installed.client_id")
	secret        = flag.String("secret", "", "installed.client_secret")
	project       = flag.String("project", "", "installed.project_id")
	auth_uri      = flag.String("auth_uri", "", "installed.auth_uri")
	token_uri     = flag.String("token_uri", "", "installed.token_uri")
	redirect      = flag.String("redirect", "", "installed.redirect_uris[]")
	access_token  = flag.String("access_token", "", "token.access_token")
	refresh_token = flag.String("refresh_token", "", "token.refresh_token")
	token_type    = flag.String("token_type", "", "token.token_type")
	root_folder   = flag.String("root_folder", "", "Google drive folder id (gdrive:<ID>) or path to folder")

	gdriveConfig drivefs.GoogleOauthConfig
)

func main() {
	flag.Parse()
	gdriveConfig.Client = *client
	gdriveConfig.Secret = *secret
	gdriveConfig.Project = *project
	gdriveConfig.AuthURI = *auth_uri
	gdriveConfig.TokenURI = *token_uri
	gdriveConfig.Redirect = *redirect
	gdriveConfig.AccessToken = *access_token
	gdriveConfig.RefreshToken = *refresh_token
	gdriveConfig.TokenType = *token_type
	gdriveConfig.RootFolder = *root_folder

	fileConfig, err := os.ReadFile(*configPath)
	if err == nil {
		if err = json.Unmarshal(fileConfig, &gdriveConfig); err != nil {
			fmt.Fprintf(os.Stderr, "Cannot unmarshall config: %s\n", err)
			os.Exit(1)
			return
		}
	} else if os.IsNotExist(err) {
	} else {
		fmt.Fprintf(os.Stderr, "Cannot open %q: %s\n", *configPath, err)
		os.Exit(1)
		return
	}

	if *setupAuth {
		ln, err := net.Listen("tcp", ":0")
		if err != nil {
			panic(err)
		}
		P, _ := netip.ParseAddrPort(ln.Addr().String())
		ln.Close()

		config := &oauth2.Config{
			ClientID:     gdriveConfig.Client,
			ClientSecret: gdriveConfig.Secret,
			RedirectURL:  fmt.Sprintf("http://localhost:%d/callback", P.Port()),
			Scopes:       []string{drive.DriveScope, drive.DriveFileScope},
			Endpoint: oauth2.Endpoint{
				AuthURL:  gdriveConfig.AuthURI,
				TokenURL: gdriveConfig.TokenURI,
			},
		}

		var (
			server      *http.Server
			GoogleToken *oauth2.Token
		)

		mux := http.NewServeMux()
		mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
			authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
			http.Redirect(w, r, authURL, http.StatusTemporaryRedirect)
		})

		mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			if code := r.URL.Query().Get("code"); code != "" {
				if GoogleToken, err = config.Exchange(context.TODO(), code); err != nil {
					panic(fmt.Errorf("unable to retrieve token from web %v", err))
				}

				defer server.Close()
				w.WriteHeader(200)
				fmt.Fprintf(w, "<html><body>Code: %q</body></html>", code)
				fmt.Printf("Code: %q\n", code)
				return
			}
			w.WriteHeader(400)
			w.Write([]byte("Wait to code\n"))
		})

		fmt.Printf("Go to the following link in your browser then type the authorization code: \nhttp://%s/token\n", P.String())
		server = &http.Server{Addr: P.String(), Handler: mux}
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			panic(err)
		}

		gdriveConfig.AccessToken = GoogleToken.AccessToken
		gdriveConfig.RefreshToken = GoogleToken.RefreshToken
		gdriveConfig.TokenType = GoogleToken.TokenType
		gdriveConfig.Expire = GoogleToken.Expiry

		data, err := json.MarshalIndent(gdriveConfig, "", "  ")
		if err != nil {
			panic(err)
		} else if err = os.WriteFile(*configPath, data, 0666); err != nil {
			panic(err)
		}
	}

	gdrive, err := drivefs.NewGoogleDrive(gdriveConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot open gdrive client: %s\n", err)
		os.Exit(1)
		return
	}

	fmt.Printf("server listening on :%d\n", *serverPort)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", *serverPort), http.FileServerFS(gdrive)); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

}
