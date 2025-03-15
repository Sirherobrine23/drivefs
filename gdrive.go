package drivefs

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"golang.org/x/net/http2"
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

	GDocsMime = []string{
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
)

type Fs interface {
	fs.FS
	fs.StatFS
	fs.ReadDirFS
	fs.ReadFileFS
	fs.SubFS
}

type Gdrive struct {
	GoogleConfig *oauth2.Config `json:"client"` // Google client app oauth project
	GoogleToken  *oauth2.Token  `json:"token"`  // Authenticated user

	driveService *drive.Service           // Google drive service
	rootDrive    *drive.File              // Root to find files
	cache        cache.Cache[*drive.File] // Cache struct
}

// GoogleOauthConfig represents google oauth token for drive setup
type GoogleOauthConfig struct {
	Client       string                   `json:"client,omitempty"`        // installed.client_id
	Secret       string                   `json:"secret,omitempty"`        // installed.client_secret
	Project      string                   `json:"project,omitempty"`       // installed.project_id
	AuthURI      string                   `json:"auth_uri,omitempty"`      // installed.auth_uri
	TokenURI     string                   `json:"token_uri,omitempty"`     // installed.token_uri
	Redirect     string                   `json:"redirect,omitempty"`      // installed.redirect_uris[]
	AccessToken  string                   `json:"access_token,omitempty"`  // token.access_token
	RefreshToken string                   `json:"refresh_token,omitempty"` // token.refresh_token
	TokenType    string                   `json:"token_type,omitempty"`    // token.token_type
	Expire       time.Time                `json:"expire,omitzero"`         // token.expiry
	RootFolder   string                   `json:"root_folder,omitempty"`   // Google drive folder id (gdrive:<ID>) or path to folder
	Cacher       cache.Cache[*drive.File] `json:"-"`                       // Cache struct
}

// Create new Gdrive struct and configure google drive client
func NewGoogleDrive(config GoogleOauthConfig) (Fs, error) {
	// Make cache in memory if not set cache
	if config.Cacher == nil {
		config.Cacher = cache.NewMemory[*drive.File]()
	}

	gdrive := &Gdrive{
		cache: config.Cacher,
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
		} else if gdrive.rootDrive, err = gdrive.mkdirAllNodes(strings.Join(n, "/")); err != nil {
			return nil, err
		}

		// resolve and create path not exists in new root
		if len(n) >= 1 {
			if gdrive.rootDrive, err = gdrive.mkdirAllNodes(strings.Join(n, "/")); err != nil {
				return nil, err
			}
		}
	} else if gdrive.rootDrive, err = gdrive.driveService.Files.Get("root").Fields("*").Do(); err != nil {
		return nil, fmt.Errorf("cannot get root: %v", err)
	}

	return gdrive, nil
}

func (gdrive *Gdrive) cacheDelete(path string) {
	gdrive.cache.Delete(fmt.Sprintf("gdrive:%q:%s", gdrive.fixPath(path), gdrive.rootDrive.Id))
}

func (gdrive *Gdrive) cachePut(path string, node *drive.File) {
	gdrive.cache.Set(time.Hour, fmt.Sprintf("gdrive:%s:%s", gdrive.rootDrive.Id, gdrive.fixPath(path)), node)
}

func (gdrive *Gdrive) cacheGet(path string) *drive.File {
	if node, err := gdrive.cache.Get(fmt.Sprintf("gdrive:%s:%s", gdrive.rootDrive.Id, gdrive.fixPath(path))); err == nil && node != nil {
		return node
	}
	return nil
}

// Get Node info and is not trashed/deleted
func (gdrive *Gdrive) resolveNode(folderID, name string) (*drive.File, error) {
	if name == "." || name == "/" {
		return gdrive.rootDrive, nil
	}

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

		for nodeIndex := range res.Files {
			if !slices.Contains(GDocsMime, res.Files[nodeIndex].MimeType) {
				nodes = append(nodes, res.Files[nodeIndex])
			}
		}

		if folderGdrive.PageToken(res.NextPageToken); res.NextPageToken == "" {
			break
		}
	}
	return nodes, nil
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

// Resolve node path from last node to fist/root path
func (gdrive *Gdrive) forwardPathResove(nodeID string) (string, error) {
	pathNodes, fistNode, currentNode, err := []string{}, (*drive.File)(nil), (*drive.File)(nil), error(nil)
	for {
		if currentNode, err = gdrive.driveService.Files.Get(nodeID).Fields("*").Do(); err != nil {
			break
		}

		// Loop to check if is shortcut
		for limit := 200_000; limit > 0 && currentNode.MimeType == GoogleDriveMimeSyslink; limit-- {
			if currentNode, err = gdrive.driveService.Files.Get(currentNode.ShortcutDetails.TargetId).Fields("*").Do(); err != nil {
				break
			}
		}

		parents := len(currentNode.Parents)
		if parents == 0 {
			break // Stop count
		} else if parents > 1 {
			parentsNode, node := []*drive.File{}, (*drive.File)(nil)
			for _, parentID := range currentNode.Parents {
				if node, err = gdrive.driveService.Files.Get(parentID).Fields("*").Do(); err != nil {
					break
				}
				parentsNode = append(parentsNode, node)
			}
			slices.SortFunc(parentsNode, func(i, j *drive.File) int {
				ia, _ := time.Parse(time.RFC3339, i.CreatedTime)
				ja, _ := time.Parse(time.RFC3339, j.CreatedTime)
				return ia.Compare(ja)
			})
			currentNode = parentsNode[0]
		}

		if currentNode.Parents[0] == gdrive.rootDrive.Id {
			break // Break loop
		}
		nodeID = currentNode.Parents[0]                 // set new nodeID
		pathNodes = append(pathNodes, currentNode.Name) // Append name to path
		if fistNode == nil {
			fistNode = currentNode
		}
	}

	slices.Reverse(pathNodes)
	nodePath := path.Join(pathNodes...)
	if err == nil {
		gdrive.cachePut(nodePath, fistNode) // Save path to cache
	}

	return nodePath, err
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

// Get file stream, if error check if is http2 error to make new request
func (gdrive *Gdrive) getRequest(node *drive.FilesGetCall) (*http.Response, error) {
	node.AcknowledgeAbuse(true)
	res, err := node.Download()
	for i := 0; i < 10 && err != nil; i++ {
		if res != nil && res.StatusCode == http.StatusTooManyRequests {
			<-time.After(time.Minute) // Wait minutes to reset www.google.com/sorry/index
			res, err = node.Download()
			continue
		}

		if urlError, ok := err.(*url.Error); ok {
			if _, ok := urlError.Err.(http2.GoAwayError); ok || reflect.TypeOf(urlError.Err).String() == "http.http2GoAwayError" {
				<-time.After(time.Microsecond * 2) // Wait seconds to retry download, to google server close connection
				res, err = node.Download()
				continue
			}
		}
		break
	}
	return res, err
}
