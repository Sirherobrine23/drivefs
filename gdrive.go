package drivefs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/oauth2"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

var ErrGoogletoken = errors.New("require oauth2.Token in struct")

const (
	MimeFolder  string = "application/vnd.google-apps.folder"   // Folder mimetype
	MimeSyslink string = "application/vnd.google-apps.shortcut" // Syslink/hardlink
)

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
	GoogleOAuth GoogleApp    `json:"installed"`
	GoogleToken *oauth2.Token `json:"token,omitempty"`

	gDrive *drive.Service

	rootDrive *drive.File // My drive folder id
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

	return gdrive, nil
}

// Get all files in folder including folders
func (gdrive *Gdrive) listFiles(folderID string) ([]*drive.File, error) {
	var files = make([]*drive.File, 0)
	list := gdrive.gDrive.Files.List().Q(fmt.Sprintf("'%s' in parents", folderID)).PageSize(1000)
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
	}

	if fpath == "." || fpath == "/" {
		return gdrive.rootDrive, nil
	} else if fpath[0] == '.' && fpath[1] != '.' {
		fpath = "/" + strings.Join(strings.Split(fpath, "")[1:], "")
	}
	if gdrive.rootDrive == nil {
		var err error
		if gdrive.rootDrive, err = gdrive.gDrive.Files.Get("root").Do(); err != nil {
			return nil, err
		}
	}

	var spaths = strings.Split(strings.ReplaceAll(strings.ReplaceAll(filepath.Clean(fpath), "\\\\", "\\"), "\\", "/"), "/")
	var current, previus *drive.File = gdrive.rootDrive, nil
	for spathIndex := range spaths {
		if (spaths[spathIndex] == "" && spathIndex == 0) || spaths[spathIndex] == "~" || spaths[spathIndex] == "." || spaths[spathIndex] == "/" {
			continue
		}

		// List files
		files, err := gdrive.listFiles(current.Id)
		if err != nil {
			return nil, err
		}

		// Check to current folder exists node path
		if !slices.Contains(maping(files, func(a *drive.File) string { return a.Name }), spaths[spathIndex]) {
			return nil, fs.ErrNotExist
		}

		for _, gfile := range files {
			if gfile.Name == spaths[spathIndex] {
				previus = current // Setting old node
				current = gfile // replace to new node
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
		entrys = append(entrys, (&FileNode{file}).FsInfoDir(gdrive.gDrive))
	}
	return entrys, nil
}

func (gdrive *Gdrive) Open(fpath string) (fs.File, error) {
	file, err := gdrive.resolvePath(fpath)
	if err != nil {
		return nil, err
	}
	return (&FileNode{file}).FsInfo(gdrive.gDrive), nil
}
