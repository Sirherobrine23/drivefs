package drivefs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"golang.org/x/oauth2"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

var ErrGoogletoken = errors.New("require oauth2.Token in struct")

const (
	MimeFolder  string = "application/vnd.google-apps.folder"   // Folder mimetype
	MimeSyslink string = "application/vnd.google-apps.shortcut" // Syslink/hardlink

	searchInFolder string = "'%s' in parents"                                                     // Search any files in folder
	searchFolders  string = "mimeType = 'application/vnd.google-apps.folder' and '%s' in parents" // Get only folder's in folder
)

type GdriveOauthInfo struct {
	Client    string   `json:"client_id"`
	Secret    string   `json:"client_secret"`
	Project   string   `json:"project_id"`
	AuthURI   string   `json:"auth_uri"`
	TokenURI  string   `json:"token_uri"`
	CertURI   string   `json:"auth_provider_x509_cert_url"`
	Redirects []string `json:"redirect_uris"`
}

type Gdrive struct {
	GoogleOAuth GdriveOauthInfo `json:"installed"`
	GoogleToken *oauth2.Token   `json:"token,omitempty"`

	gDrive *drive.Service

	rootDrive *drive.File // My drive folder id
	rootNode  *FileNode   // Root node
}

func (gdrive *Gdrive) Setup() (err error) {
	if gdrive.GoogleToken == nil {
		return ErrGoogletoken
	}

	ctx := context.Background()
	config := &oauth2.Config{ClientID: gdrive.GoogleOAuth.Client, ClientSecret: gdrive.GoogleOAuth.Secret, RedirectURL: gdrive.GoogleOAuth.Redirects[0], Scopes: []string{drive.DriveScope, drive.DriveFileScope}, Endpoint: oauth2.Endpoint{AuthURL: gdrive.GoogleOAuth.AuthURI, TokenURL: gdrive.GoogleOAuth.TokenURI}}
	if gdrive.gDrive, err = drive.NewService(ctx, option.WithHTTPClient(config.Client(ctx, gdrive.GoogleToken))); err != nil {
		return err
	} else if gdrive.rootDrive, err = gdrive.gDrive.Files.Get("root").Fields("*").Do(); err != nil {
		return fmt.Errorf("cannot get root (my drive) id: %v", err)
	}

	rnodes, err := gdrive.getFoldersNode(gdrive.rootDrive.Id)
	if err != nil {
		return err
	}
	gdrive.rootNode = &FileNode{Node: gdrive.rootDrive, Childs: rnodes}
	return nil
}

func (gdrive *Gdrive) getFoldersNode(folder string) ([]*FileNode, error) {
	var files []*drive.File

	ff := gdrive.gDrive.Files.List().Q(fmt.Sprintf(searchInFolder, folder)).PageSize(1000).Fields("*")
	for {
		res, err := ff.Do()
		if err != nil {
			return nil, err
		}

		files = append(files, res.Files...)
		if res.NextPageToken != "" {
			ff.PageToken(res.NextPageToken)
			continue
		}
		break
	}

	var nodes []*FileNode
	for _, ff := range files {
		var rrNode []*FileNode
		if ff.MimeType == MimeFolder {
			var err error
			rrNode, err = gdrive.getFoldersNode(ff.Id)
			if err != nil {
				return nodes, err
			}
		}

		fmt.Println(ff.Name)
		nodes = append(nodes, &FileNode{
			Node:   ff,
			Childs: rrNode,
		})
	}

	return nodes, nil
}

func (gdrive *Gdrive) Open(path string) (fs.File, error) {
	if path == "." || path == "/" {
		return gdrive.rootNode.FsInfo(gdrive.gDrive), nil
	} else if node := gdrive.rootNode.ReverseNode(path); node != nil {
		return node.FsInfo(gdrive.gDrive), nil
	}
	return nil, fs.ErrNotExist
}

func (gdrive *Gdrive) ReadDir(path string) ([]fs.DirEntry, error) {
	var v []fs.DirEntry
	if path == "." || path == "/" {
		for index := range gdrive.rootNode.Childs {
			v = append(v, gdrive.rootNode.Childs[index].FsInfoDir(gdrive.gDrive))
		}
		return v, nil
	} else if node := gdrive.rootNode.ReverseNode(path); node != nil {
		for index := range node.Childs {
			v = append(v, node.Childs[index].FsInfoDir(gdrive.gDrive))
		}
		return v, nil
	}
	return nil, fs.ErrNotExist
}
