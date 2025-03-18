package drivefs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path"
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
	GoogleDriveMimeFile     string = "application/octet-stream"                          // File stream mime type
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

// Struct with implements [io/fs.FS]
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
func NewGoogleDrive(config GoogleOauthConfig) (FS, error) {
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
		} else if gdrive.rootDrive, err = gdrive.createNodeFolderRecursive(strings.Join(n, "/")); err != nil {
			return nil, err
		}

		// resolve and create path not exists in new root
		if len(n) >= 1 {
			if gdrive.rootDrive, err = gdrive.createNodeFolderRecursive(strings.Join(n, "/")); err != nil {
				return nil, err
			}
		}
	} else if gdrive.rootDrive, err = gdrive.driveService.Files.Get("root").Fields("*").Do(); err != nil {
		return nil, fmt.Errorf("cannot get root: %v", err)
	}

	return gdrive, nil
}

func (gdrive *Gdrive) cacheDelete(path string) {
	gdrive.cache.Delete(fmt.Sprintf("gdrive:%q:%s", pathManipulate(path).CleanPath(), gdrive.rootDrive.Id))
}

func (gdrive *Gdrive) cachePut(path string, node *drive.File) {
	gdrive.cache.Set(time.Hour, fmt.Sprintf("gdrive:%s:%s", gdrive.rootDrive.Id, pathManipulate(path).CleanPath()), node)
}

func (gdrive *Gdrive) cacheGet(path string) *drive.File {
	if node, err := gdrive.cache.Get(fmt.Sprintf("gdrive:%s:%s", gdrive.rootDrive.Id, pathManipulate(path).CleanPath())); err == nil && node != nil {
		return node
	}
	return nil
}

// Get Node info and is not trashed/deleted
func (gdrive *Gdrive) resolveNode(folderID, name string) (*drive.File, error) {
	name = strings.ReplaceAll(strings.ReplaceAll(name, `\`, `\\`), `'`, `\'`)
	file, err := gdrive.driveService.Files.List().Fields("*").PageSize(300).Q(fmt.Sprintf(GoogleListQueryWithName, folderID, name)).Do()
	if err != nil {
		return nil, ProcessErr(nil, err)
	}

	if len(file.Files) != 1 {
		return nil, fs.ErrNotExist
	} else if file.Files[0].Trashed {
		return file.Files[0], fs.ErrNotExist
	}
	return file.Files[0], nil
}

// List all files in folder
func (gdrive *Gdrive) listFiles(folderID string) ([]*drive.File, error) {
	folder, nodes := gdrive.driveService.Files.List().Fields("*").Q(fmt.Sprintf(GoogleListQuery, folderID)).PageSize(1000), []*drive.File{}
	for {
		res, err := folder.Do()
		if err != nil {
			return nodes, ProcessErr(nil, err)
		}

		for nodeIndex := range res.Files {
			if !slices.Contains(GDocsMime, res.Files[nodeIndex].MimeType) {
				nodes = append(nodes, res.Files[nodeIndex])
			}
		}

		if folder.PageToken(res.NextPageToken); res.NextPageToken == "" {
			break
		}
	}
	return nodes, nil
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
func (gdrive *Gdrive) getNode(name string) (*drive.File, error) {
	if pathManipulate(name).IsRoot() {
		return gdrive.rootDrive, nil
	}

	var current *drive.File
	if current = gdrive.cacheGet(name); current != nil {
		return current, nil
	}

	current = gdrive.rootDrive                // root
	nodes := pathManipulate(name).SplitPath() // split node
	for _, currentNode := range nodes {
		previus := current // storage previus Node
		if current = gdrive.cacheGet(currentNode[0]); current != nil {
			continue // continue to next node
		}

		var err error
		// Check if ared exist in folder
		if current, err = gdrive.resolveNode(previus.Id, currentNode[1]); err != nil {
			return nil, err // return drive error
		}
		gdrive.cachePut(currentNode[0], current)
	}
	return current, nil
}

// Get file stream, if error check if is http2 error to make new request
func (gdrive *Gdrive) getRequest(node *drive.FilesGetCall) (*http.Response, error) {
	node.AcknowledgeAbuse(true)
	res, err := node.Download()
	for i := 0; i < 3 && err != nil; i++ {
		err = ProcessErr(res, err)
		switch err {
		case fs.ErrNotExist:
			return nil, err
		case fs.ErrPermission:
			<-time.After(time.Microsecond * 2) // Wait seconds to retry download, to google server close connection
			res, err = node.Download()
		default:
			switch v := err.(type) {
			case http2.GoAwayError:
				<-time.After(time.Microsecond * 2) // Wait seconds to retry download, to google server close connection
				res, err = node.Download()
			default:
				return res, v
			}
		}
	}
	return res, err
}

func (gdrive *Gdrive) createNodeFolder(path string) (folderNode *drive.File, err error) {
	if cacheNode := gdrive.cacheGet(path); cacheNode != nil {
		return cacheNode, nil
	}

	nodes := pathManipulate(path).SplitPath()
	previusNode, lastNode := nodes.At(-2), nodes.At(-1)

	if folderNode = gdrive.cacheGet(previusNode.Path()); folderNode == nil {
		if folderNode, err = gdrive.resolveNode(gdrive.rootDrive.Id, previusNode.Path()); err != nil {
			return
		}
		gdrive.cachePut(previusNode.Path(), folderNode)
	}

	previus := folderNode
	if folderNode, err = gdrive.resolveNode(gdrive.rootDrive.Id, lastNode.Path()); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err // return drive error
		}

		nodeCreate := &drive.File{
			Name:     lastNode.Name(),       // Folder name
			MimeType: GoogleDriveMimeFolder, // folder mime
			Parents:  []string{previus.Id},  // previus to folder to create
		}

		if folderNode, err = gdrive.driveService.Files.Create(nodeCreate).Fields("*").Do(); err != nil {
			return nil, ProcessErr(nil, err)
		}
	}
	gdrive.cachePut(lastNode.Path(), folderNode)
	return
}

// Create recursive directory if not exists
func (gdrive *Gdrive) createNodeFolderRecursive(path string) (*drive.File, error) {
	if cacheNode := gdrive.cacheGet(path); cacheNode != nil {
		return cacheNode, nil
	}

	current, err, seq := gdrive.rootDrive, error(nil), pathManipulate(path).SplitPathSeq()
	for folderPath := range seq {
		previus := current // storage previus Node
		if current = gdrive.cacheGet(folderPath); current != nil {
			continue // continue to next node
		} else if current, err = gdrive.resolveNode(previus.Id, folderPath); err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				return nil, err // return drive error
			}

			// Base to create folder
			nodeCreate := &drive.File{
				MimeType: GoogleDriveMimeFolder, // folder mime
				Parents:  []string{previus.Id},  // previus to folder to create
			}

			// Create recursive folder
			for folderPath, name := range seq {
				nodeCreate.Name = name // folder name
				if current, err = gdrive.driveService.Files.Create(nodeCreate).Fields("*").Do(); err != nil {
					return nil, ProcessErr(nil, err)
				}

				gdrive.cachePut(folderPath, current) // Cache folder path
				nodeCreate.Parents[0] = current.Id   // Set new root
			}
			// Break loop and return current node
			break
		}

		// cache folder if not seted in cache
		gdrive.cachePut(folderPath, current)
	}
	return current, nil
}
