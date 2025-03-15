package drivefs

import (
	"io"
	"io/fs"

	"google.golang.org/api/drive/v3"
)

func (gdrive *Gdrive) ReadLink(name string) (string, error) {
	fileNode, err := gdrive.getNode(name)
	if err != nil {
		return "", err
	}

	// Loop to check if is shortcut
	for limit := 200_000; limit > 0 && fileNode.MimeType == GoogleDriveMimeSyslink; limit-- {
		if fileNode, err = gdrive.driveService.Files.Get(fileNode.ShortcutDetails.TargetId).Fields("*").Do(); err != nil {
			return "", err
		}
	}

	return gdrive.forwardPathResove(fileNode.Id)
}

func (gdrive *Gdrive) Lstat(name string) (fs.FileInfo, error) {
	fileNode, err := gdrive.getNode(name)
	if err != nil {
		return nil, err
	}
	return &Stat{fileNode}, nil
}

// Resolve path and return File or Folder Stat
func (gdrive *Gdrive) Stat(path string) (fs.FileInfo, error) {
	fileNode, err := gdrive.getNode(path)
	if err != nil {
		return nil, err
	}

	// Loop to check if is shortcut
	for limit := 200_000; limit > 0 && fileNode.MimeType == GoogleDriveMimeSyslink; limit-- {
		if fileNode, err = gdrive.driveService.Files.Get(fileNode.ShortcutDetails.TargetId).Do(); err != nil {
			return nil, err
		}
	}

	return &Stat{fileNode}, nil
}

// List files and folder in Directory
func (gdrive *Gdrive) ReadDir(name string) ([]fs.DirEntry, error) {
	current, err := (*drive.File)(nil), error(nil)
	if current, err = gdrive.getNode(name); err != nil {
		return nil, err
	}

	nodes, err := gdrive.listNodes(current.Id)
	if err != nil {
		return nil, err
	}

	entrysSlice := []fs.DirEntry{}
	for index := range nodes {
		entrysSlice = append(entrysSlice, fs.FileInfoToDirEntry(&Stat{nodes[index]}))
	}

	return entrysSlice, nil
}

// resolve path and return File stream
func (gdrive *Gdrive) Open(name string) (fs.File, error) {
	node, err := gdrive.getNode(name)
	if err != nil {
		return nil, err
	}

	if node.MimeType == GoogleDriveMimeFolder {
		nodes, err := gdrive.listNodes(node.Id)
		if err != nil {
			return nil, err
		}

		nodeFiles := []fs.DirEntry{}
		for _, node := range nodes {
			nodeFiles = append(nodeFiles, fs.FileInfoToDirEntry(&Stat{File: node}))
		}
		return &GdriveNode{filename: name, gClient: gdrive, node: node, nodeFiles: nodeFiles, filesOffset: 0, direction: DirectionWrite}, nil
	}

	boot, err := gdrive.getRequest(gdrive.driveService.Files.Get(node.Id))
	if err != nil {
		return nil, err
	}

	return &GdriveNode{
		filename:  name,
		gClient:   gdrive,
		node:      node,
		sReadRes:  boot,
		direction: DirectionReader,
	}, nil
}

func (gdrive Gdrive) ReadFile(name string) ([]byte, error) {
	file, err := gdrive.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(file)
}
