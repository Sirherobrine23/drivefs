package drivefs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"golang.org/x/oauth2"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

var ErrGoogletoken = errors.New("require oauth2.Token in struct")

type GoogleApp struct {
	Client    string   `json:"client_id"`
	Secret    string   `json:"client_secret"`
	Project   string   `json:"project_id"`
	AuthURI   string   `json:"auth_uri"`
	TokenURI  string   `json:"token_uri"`
	CertURI   string   `json:"auth_provider_x509_cert_url"`
	Redirects []string `json:"redirect_uris"`
}

type Gdrive struct {
	GoogleOAuth GoogleApp     `json:"installed"`       // Google oauth app
	GoogleToken *oauth2.Token `json:"token,omitempty"` // User authe token

	gDrive *drive.Service

	rootDrive    *drive.File              // My drive folder id (use "root" alias to locate)
	cachrRW      sync.RWMutex             // cacheFolders Locker
	cacheFolders map[string][]*drive.File // Cache folders
}

func maping[A any, B any](input []A, fn func(imput A) B) []B {
	var cat []B
	for _, kk := range input {
		cat = append(cat, fn(kk))
	}
	return cat
}

// Create new Gdrive struct and configure google drive client
func New(app GoogleApp, gToken oauth2.Token) (*Gdrive, error) {
	config := &oauth2.Config{ClientID: app.Client, ClientSecret: app.Secret, RedirectURL: app.Redirects[0], Scopes: []string{drive.DriveScope, drive.DriveFileScope}, Endpoint: oauth2.Endpoint{AuthURL: app.AuthURI, TokenURL: app.TokenURI}}
	ctx := context.Background()
	var gdrive *Gdrive = new(Gdrive)
	gdrive.GoogleOAuth = app
	gdrive.GoogleToken = new(oauth2.Token)
	*gdrive.GoogleToken = gToken

	var err error
	if gdrive.gDrive, err = drive.NewService(ctx, option.WithHTTPClient(config.Client(ctx, gdrive.GoogleToken))); err != nil {
		return nil, err
	} else if gdrive.rootDrive, err = gdrive.gDrive.Files.Get("root").Fields("*").Do(); err != nil {
		return nil, fmt.Errorf("cannot get root (my drive) id: %v", err)
	}

	gdrive.cacheFolders = make(map[string][]*drive.File)
	return gdrive, nil
}

// Get all files in folder including folders
func (gdrive *Gdrive) listFiles(folderID string) ([]*drive.File, error) {
	var files = make([]*drive.File, 0)
	list := gdrive.gDrive.Files.List().Fields("*").Q(fmt.Sprintf("'%s' in parents", folderID)).PageSize(1000)
	for {
		res, err := list.Do()
		if err != nil {
			return files, err
		}
		files = append(files, res.Files...)
		if res.NextPageToken == "" {
			break
		}
		list.PageToken(res.NextPageToken)
	}
	return files, nil
}

func (gdrive *Gdrive) resolvePath(fpath string) (*drive.File, error) {
	if gdrive.gDrive == nil {
		return nil, fmt.Errorf("cannot get google drive endpoints")
	} else if gdrive.rootDrive == nil {
		var err error
		if gdrive.rootDrive, err = gdrive.gDrive.Files.Get("root").Fields("*").Do(); err != nil {
			return nil, err
		}
	}

	fpath = strings.ReplaceAll(strings.ReplaceAll(filepath.Clean(fpath), "\\\\", "\\"), "\\", "/")
	if fpath == "." || fpath == "/" || fpath == "./" {
		return gdrive.rootDrive, nil
	} else if fpath[0] == '.' && fpath[1] == '/' {
		fpath = strings.Join(strings.Split(fpath, "")[2:], "")
	}

	var current, previus *drive.File = gdrive.rootDrive, nil
	var spaths = strings.Split(fpath, "/")
	for spathIndex := range spaths {
		if spaths[spathIndex] == "" || (spaths[spathIndex] == "~" || spaths[spathIndex] == "." || spaths[spathIndex] == "/") && spathIndex == 0 {
			continue
		}

		if gdrive.cacheFolders == nil {
			gdrive.cachrRW.Lock()
			gdrive.cacheFolders = make(map[string][]*drive.File)
			gdrive.cachrRW.Unlock()
		}

		gdrive.cachrRW.RLock()
		files, ok := gdrive.cacheFolders[current.Id]
		gdrive.cachrRW.RUnlock()
		if !ok {
			// List files
			var err error
			if files, err = gdrive.listFiles(current.Id); err != nil {
				return nil, err
			}

			gdrive.cachrRW.Lock()
			gdrive.cacheFolders[current.Id] = files
			gdrive.cachrRW.Unlock()
		}

		// Check to current folder exists node path
		if !slices.Contains(maping(files, func(a *drive.File) string { return a.Name }), spaths[spathIndex]) {
			return nil, fs.ErrNotExist
		}

		for _, gfile := range files {
			if gfile.Name == spaths[spathIndex] {
				previus = current // Setting old node
				current = gfile   // replace to new node
				break
			}
		}
	}

	if previus == nil && current != nil {
		return current, nil
	} else if previus != nil && current == nil {
		return nil, fs.ErrNotExist
	} else if previus != nil && current != nil {
		if previus.Id != current.Id {
			return current, nil
		}
	}
	return nil, fs.ErrNotExist
}

func (gdrive *Gdrive) ReadDir(fpath string) ([]fs.DirEntry, error) {
	folder, err := gdrive.resolvePath(fpath)
	if err != nil {
		return nil, err
	} else if folder == nil {
		return nil, fmt.Errorf("cannot get folder")
	}

	files, err := gdrive.listFiles(folder.Id)
	if err != nil {
		return nil, err
	}

	var entrys []fs.DirEntry
	for _, file := range files {
		fsStatt, err := fsStat(file)
		if err != nil {
			return nil, err
		}
		entrys = append(entrys, fs.FileInfoToDirEntry(fsStatt))
	}
	return entrys, nil
}

func (gdrive *Gdrive) Open(fpath string) (fs.File, error) {
	file, err := gdrive.resolvePath(fpath)
	if err != nil {
		return nil, err
	}
	return fsInfo(file, gdrive), nil
}

func (gdrive *Gdrive) ReadFile(fpath string) ([]byte, error) {
	file, err := gdrive.resolvePath(fpath)
	if err != nil {
		return nil, err
	}
	res, err := gdrive.gDrive.Files.Get(file.Id).Download()
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	return io.ReadAll(res.Body)
}

func (gdrive *Gdrive) Stat(fpath string) (fs.FileInfo, error) {
	file, err := gdrive.resolvePath(fpath)
	if err != nil {
		return nil, err
	}
	return fsStat(file)
}

// Fork gdrive app and set different `rootDrive` from `my drive` to Folder, only FOLDER
//
// Sub returns an FS corresponding to the subtree rooted at dir.
func (gdrive *Gdrive) Sub(fpath string) (fs.FS, error) {
	folder, err := gdrive.resolvePath(fpath)
	if err != nil {
		return nil, err
	} else if folder.MimeType != MimeFolder {
		return nil, fs.ErrInvalid
	}

	// Clone app struct
	return &Gdrive{GoogleOAuth: gdrive.GoogleOAuth, GoogleToken: gdrive.GoogleToken, gDrive: gdrive.gDrive, cacheFolders: gdrive.cacheFolders, rootDrive: folder}, nil
}