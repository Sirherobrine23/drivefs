package drivefs

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
	"sirherobrine23.com.br/Sirherobrine23/drivefs/cache"
)

const (
	GoogleListQueryWithName string = "trashed=false and '%s' in parents and name = '%s'" // Query files list with name
	GoogleListQuery         string = "trashed=false and '%s' in parents"                 // Query files list
	GoogleDriveMimeFolder   string = "application/vnd.google-apps.folder"                // Folder mime type
	GoogleDriveMimeSyslink  string = "application/vnd.google-apps.shortcut"              // Syslink mime type
)

var (
	_ fs.FS         = &Gdrive{}
	_ fs.StatFS     = &Gdrive{}
	_ fs.ReadDirFS  = &Gdrive{}
	_ fs.ReadFileFS = &Gdrive{}
	_ fs.SubFS      = &Gdrive{}
)

type Fs interface {
	fs.FS
	fs.StatFS
	fs.ReadDirFS
	fs.ReadFileFS
	fs.SubFS
	fs.GlobFS
}

type Gdrive struct {
	GoogleConfig *oauth2.Config // Google client app oauth project
	GoogleToken  *oauth2.Token  // Authenticated user
	driveService *drive.Service // Google drive service
	rootDrive    *drive.File    // Root to find files

	cache *cache.LocalCache[*drive.File]
}

// GoogleOauthConfig represents google oauth token for drive setup
type GoogleOauthConfig struct {
	Client       string    `json:",omitempty"`
	Secret       string    `json:",omitempty"`
	Project      string    `json:",omitempty"`
	AuthURI      string    `json:",omitempty"`
	TokenURI     string    `json:",omitempty"`
	Redirect     string    `json:",omitempty"`
	AccessToken  string    `json:",omitempty"`
	RefreshToken string    `json:",omitempty"`
	Expire       time.Time `json:",omitempty"`
	TokenType    string    `json:",omitempty"`
	RootFolder   string    `json:",omitempty"` // Google drive folder id (gdrive:<ID>) or path to folder
}

// Create new Gdrive struct and configure google drive client
func NewGoogleDrive(config GoogleOauthConfig) (*Gdrive, error) {
	gdrive := &Gdrive{
		cache: &cache.LocalCache[*drive.File]{},
		GoogleConfig: &oauth2.Config{
			ClientID:     config.Client,
			ClientSecret: config.Secret,
			RedirectURL:  config.Redirect,
			Scopes:       []string{drive.DriveScope, drive.DriveFileScope},
			Endpoint: oauth2.Endpoint{
				AuthURL:  config.AuthURI,
				TokenURL: config.TokenURI,
			},
		},
		GoogleToken: &oauth2.Token{
			AccessToken:  config.AccessToken,
			TokenType:    config.TokenType,
			RefreshToken: config.RefreshToken,
			Expiry:       config.Expire,
		},
	}

	err, ctx := error(nil), context.Background()
	if gdrive.driveService, err = drive.NewService(ctx, option.WithHTTPClient(gdrive.GoogleConfig.Client(ctx, gdrive.GoogleToken))); err != nil {
		return nil, err
	}

	if config.RootFolder != "" {
		n := strings.Split(config.RootFolder, "/")
		// Create folder with root id
		if strings.HasPrefix(n[0], "gdrive:") {
			if gdrive.rootDrive, err = gdrive.driveService.Files.Get(n[0][7:]).Fields("*").Do(); err != nil {
				return nil, fmt.Errorf("cannot get root: %v", err)
			}
			n = n[1:]
		} else if gdrive.rootDrive, err = gdrive.MkdirAll(strings.Join(n, "/")); err != nil {
			return nil, err
		}

		// resolve and create path not exists in new root
		if len(n) >= 1 {
			if gdrive.rootDrive, err = gdrive.MkdirAll(strings.Join(n, "/")); err != nil {
				return nil, err
			}
		}
	} else if gdrive.rootDrive, err = gdrive.driveService.Files.Get("root").Fields("*").Do(); err != nil {
		return nil, fmt.Errorf("cannot get root: %v", err)
	}

	return gdrive, nil
}

func (gdrive *Gdrive) cacheDelete(path string) {
	gdrive.cache.Delete(fmt.Sprintf("gdrive:%s:%s", gdrive.rootDrive.Id, gdrive.fixPath(path)))
}

func (gdrive *Gdrive) cachePut(path string, node *drive.File) {
	gdrive.cache.Set(time.Now().Add(time.Hour), fmt.Sprintf("gdrive:%s:%s", gdrive.rootDrive.Id, gdrive.fixPath(path)), node)
}

func (gdrive *Gdrive) cacheGet(path string) *drive.File {
	if node, ok := gdrive.cache.Get(fmt.Sprintf("gdrive:%s:%s", gdrive.rootDrive.Id, gdrive.fixPath(path))); ok && node != nil {
		return node
	}
	return nil
}

// Get Node info and is not trashed/deleted
func (gdrive *Gdrive) resolveNode(folderID, name string) (*drive.File, error) {
	name = strings.ReplaceAll(strings.ReplaceAll(name, `\`, `\\`), `'`, `\'`)
	file, err := gdrive.driveService.Files.List().Fields("*").PageSize(300).Q(fmt.Sprintf(GoogleListQueryWithName, folderID, name)).Do()
	if err != nil {
		return nil, err
	}

	if len(file.Files) != 1 {
		return nil, fs.ErrNotExist
	} else if file.Files[0].Trashed {
		return file.Files[0], fs.ErrNotExist
	}
	return file.Files[0], nil
}

// List all files in folder
func (gdrive *Gdrive) listNodes(folderID string) ([]*drive.File, error) {
	folderGdrive := gdrive.driveService.Files.List().Fields("*").Q(fmt.Sprintf(GoogleListQuery, folderID)).PageSize(1000)
	nodes := []*drive.File{}
	for {
		res, err := folderGdrive.Do()
		if err != nil {
			return nodes, err
		}
		nodes = append(nodes, res.Files...)
		if folderGdrive.PageToken(res.NextPageToken); res.NextPageToken == "" {
			break
		}
	}
	return nodes, nil
}

// Resolve node path and return New Gdrive struct
func (gdrive *Gdrive) Sub(dir string) (fs.FS, error) {
	node, err := gdrive.resolveNode(gdrive.rootDrive.Id, dir)
	if err != nil {
		return nil, err
	}

	// Return New gdrive struct
	return &Gdrive{
		cache:        gdrive.cache,
		driveService: gdrive.driveService,
		GoogleConfig: gdrive.GoogleConfig,
		GoogleToken:  gdrive.GoogleToken,
		rootDrive:    node,
	}, nil
}

// Split to nodes
func (*Gdrive) pathSplit(path string) []struct{ Name, Path string } {
	path = strings.Trim(filepath.ToSlash(path), "/")

	var nodes []struct{ Name, Path string }
	lastNode := 0
	for indexStr := range path {
		if path[indexStr] == '/' {
			nodes = append(nodes, struct{ Name, Path string }{path[lastNode:indexStr], path[0:indexStr]})
			lastNode = indexStr + 1
		}
	}
	nodes = append(nodes, struct{ Name, Path string }{path[lastNode:], path})
	return nodes
}

// Check if path have sub-folders
func (gdrive *Gdrive) checkMkdir(path string) bool { return len(gdrive.pathSplit(path)) > 1 }

// pretty path
func (gdrive *Gdrive) fixPath(path string) string { return gdrive.getLast(path).Path }

// pretty path and return last element
func (gdrive *Gdrive) getLast(path string) struct{ Name, Path string } {
	n := gdrive.pathSplit(path)
	return n[len(n)-1]
}

// Get *drive.File if exist
func (gdrive *Gdrive) getNode(path string) (*drive.File, error) {
	var current *drive.File
	if current = gdrive.cacheGet(gdrive.fixPath(path)); current != nil {
		return current, nil
	}

	current = gdrive.rootDrive      // root
	nodes := gdrive.pathSplit(path) // split node
	for _, currentNode := range nodes {
		previus := current // storage previus Node
		if current = gdrive.cacheGet(currentNode.Path); current != nil {
			continue // continue to next node
		}

		var err error
		// Check if ared exist in folder
		if current, err = gdrive.resolveNode(previus.Id, currentNode.Name); err != nil {
			return nil, err // return drive error
		}
		gdrive.cachePut(currentNode.Path, current)
	}
	return current, nil
}

// Save file in path, if folder not exists create
func (gdrive *Gdrive) Save(path string, r io.Reader) (int64, error) {
	n := gdrive.pathSplit(path)
	if stat, err := gdrive.Stat(path); err == nil {
		res, err := gdrive.driveService.Files.Update(stat.(*Stat).File.Id, nil).Media(r).Do()
		if err != nil {
			return 0, err
		}
		gdrive.cachePut(n[len(n)-1].Path, res)
		return res.Size, nil
	}

	rootSolver := gdrive.rootDrive
	if gdrive.checkMkdir(path) {
		var err error
		if rootSolver, err = gdrive.MkdirAll(n[len(n)-2].Path); err != nil {
			return 0, err
		}
	}

	var err error
	if rootSolver, err = gdrive.driveService.Files.Create(&drive.File{MimeType: "application/octet-stream", Name: n[len(n)-1].Name, Parents: []string{rootSolver.Id}}).Fields("*").Media(r).Do(); err != nil {
		return 0, err
	}
	gdrive.cachePut(n[len(n)-1].Path, rootSolver)
	return rootSolver.Size, nil
}
