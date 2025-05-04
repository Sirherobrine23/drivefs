package drivefs

import (
	"context"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
	"sirherobrine23.com.br/Sirherobrine23/drivefs/cache"
	"sirherobrine23.com.br/Sirherobrine23/drivefs/pool"
)

const (
	DefaultTTL = time.Hour * 2
	
	GoogleListQueryWithName string = "trashed=false and '%s' in parents and name = '%q'" // Query files list with name
	GoogleListQuery         string = "trashed=false and '%s' in parents"                 // Query files list
	GoogleDriveMimeFolder   string = "application/vnd.google-apps.folder"                // Folder mime type
	GoogleDriveMimeSyslink  string = "application/vnd.google-apps.shortcut"              // Syslink mime type
	GoogleDriveMimeFile     string = "application/octet-stream"                          // File stream mime type
)

var GDocsMime = []string{
	"application/vnd.google-apps.document",
	"application/vnd.google-apps.drive-sdk",
	"application/vnd.google-apps.drawing",
	"application/vnd.google-apps.form",
	"application/vnd.google-apps.fusiontable",
	"application/vnd.google-apps.jam",
	"application/vnd.google-apps.mail-layout",
	"application/vnd.google-apps.map",
	"application/vnd.google-apps.presentation",
	"application/vnd.google-apps.script",
	"application/vnd.google-apps.site",
	"application/vnd.google-apps.spreadsheet",
	"application/vnd.google-apps.unknown",
}

type Gdrive struct {
	GoogleConfig *oauth2.Config `json:"client"` // Google client app oauth project
	GoogleToken  *oauth2.Token  `json:"token"`  // Authenticated user

	RootDrive      *drive.File                // Root to find files
	Cache          cache.Cache[*drive.File]   // Cache struct
	PoolServervice *pool.Pool[*drive.Service] // Pool of google drive clients
}

func NewClient(rootFolder string, config *oauth2.Config, token *oauth2.Token) (*Gdrive, error) {
	client := &Gdrive{
		GoogleConfig: config,
		GoogleToken:  token,
		Cache:        cache.NewMemory[*drive.File](),
		PoolServervice: pool.NewPool[*drive.Service](func() (*drive.Service, error) {
			ctx := context.Background()
			return drive.NewService(ctx, option.WithHTTPClient(config.Client(ctx, token)))
		}),
	}

	gdriveClient, err := client.PoolServervice.Get()
	if err != nil {
		return nil, err
	}

	switch {
	case strings.HasPrefix(rootFolder, "gdrive:"):
		nodes := strings.Split(rootFolder, "/")
		client.RootDrive, err = gdriveClient.Files.Get(strings.TrimPrefix(nodes[0], "gdrive:")).Fields("*").Do()
		if err != nil {
			return nil, err
		}
		rootFolder = strings.Join(nodes[1:], "/")
		fallthrough
	case rootFolder != "":
		if client.RootDrive == nil {
			if client.RootDrive, err = gdriveClient.Files.Get("root").Fields("*").Do(); err != nil {
				return nil, err
			}
		}
		rootFolder = strings.Trim(rootFolder, "/")
		
	default:
		if client.RootDrive, err = gdriveClient.Files.Get("root").Fields("*").Do(); err != nil {
			return nil, err
		}
	}

	return client, nil
}
