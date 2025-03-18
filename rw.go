package drivefs

import (
	"io"
	"io/fs"

	"google.golang.org/api/drive/v3"
)

// Delete file from google drive
//
// Deprecated: use [sirherobrine23.com.br/Sirherobrine23/drivefs.Gdrive.Remove]
func (gdrive *Gdrive) Delete(name string) error {
	return gdrive.Remove(name)
}

// Link to [sirherobrine23.com.br/Sirherobrine23/drivefs.Gdrive.Remove]
func (gdrive *Gdrive) RemoveAll(name string) error {
	return gdrive.Remove(name)
}

// Delete file from google drive if is folder delete recursive
func (gdrive *Gdrive) Remove(name string) error {
	fileNode, err := gdrive.getNode(name)
	if err != nil {
		return err
	}
	gdrive.cacheDelete(name)
	return gdrive.driveService.Files.Delete(fileNode.Id).Do()
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

func (gdrive *Gdrive) Mkdir(name string) (err error) {
	_, err = gdrive.createNodeFolder(name)
	return
}

func (gdrive *Gdrive) MkdirAll(name string) (err error) {
	_, err = gdrive.createNodeFolderRecursive(name)
	return
}

// Save file in path, if folder not exists create
//
// Deprecated: use [sirherobrine23.com.br/Sirherobrine23/drivefs.Create]
func (gdrive *Gdrive) Save(name string, r io.Reader) (int64, error) {
	f, err := gdrive.Create(name)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return io.Copy(f, r)
}

// Create file if not exists, if exists delete and recreate
func (gdrive *Gdrive) Create(name string) (File, error) {
	if stat, err := gdrive.Stat(name); err == nil {
		node := stat.(*Stat).File
		if node.MimeType == GoogleDriveMimeFolder {
			return nil, fs.ErrInvalid
		} else if err = gdrive.driveService.Files.Delete(node.Id).Do(); err != nil {
			return nil, err
		}
	}

	pathNodes := pathManipulate(name).SplitPath()
	rootFolder, err := gdrive.getNode(pathNodes.At(-2).Path())
	if err != nil {
		return nil, err
	}

	fileNode, err := gdrive.driveService.Files.Create(&drive.File{
		MimeType: GoogleDriveMimeFile,     // File stream mime
		Name:     pathNodes.At(-1).Name(), // Folder name
		Parents:  []string{rootFolder.Id}, // previus to folder to create
	}).Fields("*").Do()
	if err != nil {
		return nil, ProcessErr(nil, err)
	}

	return &GdriveNode{
		filename: pathManipulate(fileNode.Name).EscapeName(),
		gClient:  gdrive,
		node:     fileNode,
		nodeRoot: rootFolder,
	}, nil
}
