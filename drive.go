package drivefs

import (
	"io/fs"
	"time"

	"golang.org/x/oauth2"
	"google.golang.org/api/drive/v2"

	drivefs "sirherobrine23.com.br/Sirherobrine23/drivefs/internal"
)

// Generic interface with all implements to [io/fs]
type FS interface {
	fs.FS
	fs.StatFS
	fs.ReadDirFS
	fs.ReadFileFS
	fs.SubFS

	// ReadLink returns the destination of the named symbolic link.
	// If there is an error, it should be of type [*io/fs.PathError].
	ReadLink(name string) (string, error)

	// Lstat returns a [io/fs.FileInfo] describing the named file.
	// If the file is a symbolic link, the returned [io/fs.FileInfo] describes the symbolic link.
	// Lstat makes no attempt to follow the link.
	// If there is an error, it should be of type [*io/fs.PathError].
	Lstat(name string) (fs.FileInfo, error)
}

// GoogleOauthConfig represents google oauth token for drive setup
type GoogleOauthConfig struct {
	Client       string    `json:"client,omitempty"`        // installed.client_id
	Secret       string    `json:"secret,omitempty"`        // installed.client_secret
	Project      string    `json:"project,omitempty"`       // installed.project_id
	AuthURI      string    `json:"auth_uri,omitempty"`      // installed.auth_uri
	TokenURI     string    `json:"token_uri,omitempty"`     // installed.token_uri
	Redirect     string    `json:"redirect,omitempty"`      // installed.redirect_uris[]
	AccessToken  string    `json:"access_token,omitempty"`  // token.access_token
	RefreshToken string    `json:"refresh_token,omitempty"` // token.refresh_token
	TokenType    string    `json:"token_type,omitempty"`    // token.token_type
	Expire       time.Time `json:"expire,omitzero"`         // token.expiry
	RootFolder   string    `json:"root_folder,omitempty"`   // Google drive folder id (gdrive:<ID>) or path to folder
}

// Google drive client
type Client struct {
	*drivefs.Gdrive
}

func CreateClient(config GoogleOauthConfig) (FS, error) {
	driveClient, err := drivefs.NewClient(
		config.RootFolder,
		&oauth2.Config{
			ClientID:     config.Client,
			ClientSecret: config.Secret,
			RedirectURL:  config.Redirect,
			Scopes:       []string{drive.DriveScope, drive.DriveFileScope},
			Endpoint: oauth2.Endpoint{
				AuthURL:  config.AuthURI,
				TokenURL: config.TokenURI,
			},
		},
		&oauth2.Token{
			AccessToken:  config.AccessToken,
			TokenType:    config.TokenType,
			RefreshToken: config.RefreshToken,
			Expiry:       config.Expire,
		})

	return &Client{driveClient}, err
}

func (*Client) Open(name string) (fs.File, error)
func (*Client) ReadDir(name string) ([]fs.DirEntry, error)
func (*Client) ReadFile(name string) ([]byte, error)
func (*Client) Stat(name string) (fs.FileInfo, error)
func (*Client) Sub(dir string) (fs.FS, error)
func (*Client) ReadLink(name string) (string, error)
func (*Client) Lstat(name string) (fs.FileInfo, error)
