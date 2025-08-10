package drivefs

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/oauth2"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	"sirherobrine23.com.br/Sirherobrine23/drivefs/cache"
)

const (
	GoogleListQueryWithName string = "trashed=false and '%s' in parents and name = '%s'" // Query files list with name
	GoogleListQuery         string = "trashed=false and '%s' in parents"                 // Query files list
	GoogleDriveMimeFolder   string = "application/vnd.google-apps.folder"                // Folder mime type
	GoogleDriveMimeSyslink  string = "application/vnd.google-apps.shortcut"              // Syslink mime type
	GoogleDriveMimeFile     string = "application/octet-stream"                          // File stream mime type
	UnixModeProperties      string = "unixMode"                                          // File permission properties

	DefaultCacheTime = time.Minute * 2
)

// Google drive mime types
var DriveMimes = []string{
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

type File interface {
	fs.File
	io.ReadWriteCloser
	io.ReaderAt
	io.WriterAt
	io.Seeker

	ReadDir(count int) ([]fs.DirEntry, error)
	Sync() error
	Truncate(size int64) error
}

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

	Statfs(path string) (total, free uint64, err error)
	Stat(path string) (fs.FileInfo, error)

	OpenFile(name string, flag int, perm fs.FileMode) (File, error)
	Create(name string) (File, error)
	ReadFile(name string) ([]byte, error)
	ReadDir(name string) ([]fs.DirEntry, error)

	Mkdir(name string, _ fs.FileMode) (err error)
	Remove(name string) error
	Rename(oldName string, newName string) error

	Sub(dir string) (fs.FS, error)
}

// Struct with implements [io/fs.FS]
type Gdrive struct {
	GoogleConfig *oauth2.Config `json:"client"`           // Google client app oauth project
	GoogleToken  *oauth2.Token  `json:"token"`            // Authenticated user
	SubDir       string         `json:"subdir,omitempty"` // Subdir to join in cache

	driveService *drive.Service             // Google drive service
	rootDrive    *drive.File                // Root to find files
	cache        cache.Cache[*drive.File]   // Cache struct
	cacheDir     cache.Cache[[]*drive.File] // Cache struct
}

type AuthFn func(ctx context.Context, config *oauth2.Config) (token *oauth2.Token, err error)

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
	UserAuth     AuthFn    `json:"-"`                       // Function to auth user
}

// Create new Gdrive struct and configure google drive client
func NewGoogleDrive(config GoogleOauthConfig) (FS, error) {
	gdrive := &Gdrive{
		cache:    cache.NewMemory[*drive.File](),
		cacheDir: cache.NewMemory[[]*drive.File](),

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
	if config.AccessToken == "" || config.RefreshToken == "" {
		if auth := config.UserAuth; auth != nil {
			if gdrive.GoogleToken, err = config.UserAuth(ctx, gdrive.GoogleConfig); err != nil {
				return nil, err
			}
		}
	}

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
		} else if gdrive.rootDrive, err = gdrive.crFolder(strings.Join(n, "/")); err != nil {
			return nil, err
		}

		// resolve and create path not exists in new root
		if len(n) >= 1 {
			if gdrive.rootDrive, err = gdrive.crFolder(strings.Join(n, "/")); err != nil {
				return nil, err
			}
		}
	} else if gdrive.rootDrive, err = gdrive.driveService.Files.Get("root").Fields("*").Do(); err != nil {
		return nil, fmt.Errorf("cannot get root: %v", err)
	}

	return gdrive, nil
}

// Get [*drive.File] from folder id without trashed and have one only
func getNodeFromFolder(driveService *drive.Service, folderID, name string) (nodeFile *drive.File, err error) {
	name = strings.ReplaceAll(strings.ReplaceAll(name, `\`, `\\`), `'`, `\'`)
	file, err := driveService.Files.List().Fields("*").PageSize(3).Q(fmt.Sprintf(GoogleListQueryWithName, folderID, name)).Do()
	if err != nil {
		return nil, ProcessErr(nil, err)
	}

	err = fs.ErrNotExist
	if len(file.Files) == 1 {
		err, nodeFile = nil, file.Files[0]
	}
	return
}

// Get file stream, if error check if is http2 error to make new request
func openFileAPI(node *drive.FilesGetCall) (*http.Response, error) {
	node.AcknowledgeAbuse(true)
	res, err := node.Download()
	var resStatus *googleapi.ServerResponse

	for i := 0; i < 3 && err != nil; i++ {
		if res != nil {
			resStatus = &googleapi.ServerResponse{HTTPStatusCode: res.StatusCode, Header: res.Header}
		}
		err = ProcessErr(resStatus, err)
		resStatus = nil

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

// List all files in folder
func (gdrive *Gdrive) filesFromNode(folderID string) ([]*drive.File, error) {
	if gdrive.cacheDir != nil {
		nodes, err := gdrive.cacheDir.Get(folderID)
		if err != nil && err != cache.ErrNotExist || len(nodes) > 0 {
			return nodes, err
		}
	}

	folder, nodes := gdrive.driveService.Files.List().Fields("*").Q(fmt.Sprintf(GoogleListQuery, folderID)).PageSize(1000), []*drive.File{}
	for {
		res, err := folder.Do()
		if err != nil {
			return nodes, ProcessErr(nil, err)
		}

		for nodeIndex := range res.Files {
			if !slices.Contains(DriveMimes, res.Files[nodeIndex].MimeType) {
				nodes = append(nodes, res.Files[nodeIndex])
			}
		}

		if folder.PageToken(res.NextPageToken); res.NextPageToken == "" {
			break
		}
	}

	if gdrive.cacheDir != nil {
		gdrive.cacheDir.Set(DefaultCacheTime, folderID, nodes)
	}

	return nodes, nil
}

// Resolve node path from last node to fist/root path
func (gdrive *Gdrive) forwardPathResolve(nodeID string) (string, error) {
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
	// Save path to cache
	if err == nil && gdrive.cache != nil {
		gdrive.cache.Set(DefaultCacheTime, path.Join(gdrive.SubDir, nodePath), fistNode)
	}

	return nodePath, ProcessErr(nil, err)
}

// Get *drive.File if exist
func (gdrive *Gdrive) getNode(name string) (current *drive.File, err error) {
	if pathManipulate(name).IsRoot() {
		return gdrive.rootDrive, nil
	} else if gdrive.cache != nil {
		if current, err = gdrive.cache.Get(path.Join(gdrive.SubDir, name)); err != nil && err != cache.ErrNotExist || current != nil {
			return
		}
	}

	// Start with root and walking in path until you get to the end of the node
	current = gdrive.rootDrive
	for filePath, name := range pathManipulate(name).SplitPathSeq() {
		previus := current // storage previus Node

		// if have in cache get and skip to next path
		if gdrive.cache != nil {
			if current, err = gdrive.cache.Get(path.Join(gdrive.SubDir, name)); err != nil && err != cache.ErrNotExist {
				return
			} else if current != nil {
				continue // continue to next node
			}
		}

		// Check if ared exist in folder
		if current, err = getNodeFromFolder(gdrive.driveService, previus.Id, name); err != nil {
			return nil, err // return drive error
		}

		if gdrive.cache != nil {
			gdrive.cache.Set(DefaultCacheTime, path.Join(gdrive.SubDir, filePath), current)
		}
	}

	return
}

// Create Folder recursive
func (gdrive *Gdrive) crFolder(name string) (node *drive.File, err error) {
	if gdrive.cache != nil {
		if node, err = gdrive.cache.Get(path.Join(gdrive.SubDir, name)); err != nil && err != cache.ErrNotExist || node != nil {
			return
		}
	}

	node = gdrive.rootDrive
	var previus *drive.File
	for folder, name := range pathManipulate(name).SplitPathSeq() {
		previus = node
		if node, err = gdrive.cache.Get(path.Join(gdrive.SubDir, folder)); err == nil && node != nil {
			continue
		} else if node, err = getNodeFromFolder(gdrive.driveService, previus.Id, name); err == nil {
			if gdrive.cache != nil {
				gdrive.cache.Set(DefaultCacheTime, path.Join(gdrive.SubDir, folder), node)
			}
			continue
		}

		node, err = gdrive.driveService.Files.Create(&drive.File{
			Name:       name,
			MimeType:   GoogleDriveMimeFolder,
			Parents:    []string{previus.Id},
			Properties: map[string]string{UnixModeProperties: strconv.Itoa(int(fs.ModeDir | 0666))},
		}).Fields("*").Do()
		if err != nil {
			return nil, ProcessErr(nil, err)
		} else if gdrive.cache != nil {
			gdrive.cache.Set(DefaultCacheTime, path.Join(gdrive.SubDir, folder), node)
		}
	}

	return
}
