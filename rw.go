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

// Create recursive directory if not exists
func (gdrive *Gdrive) mkdirAllNodes(path string) (*drive.File, error) {
	var current *drive.File
	if current = gdrive.cacheGet(gdrive.fixPath(path)); current != nil {
		return current, nil
	}

	current = gdrive.rootDrive      // root
	nodes := gdrive.pathSplit(path) // split node
	for nodeIndex, currentNode := range nodes {
		previus := current // storage previus Node
		if current = gdrive.cacheGet(currentNode.Path); current != nil {
			continue // continue to next node
		}

		var err error
		// Check if ared exist in folder
		if current, err = gdrive.resolveNode(previus.Id, currentNode.Name); err != nil {
			if err != fs.ErrNotExist {
				return nil, err // return drive error
			}

			// Base to create folder
			var folderCreate drive.File
			folderCreate.MimeType = GoogleDriveMimeFolder // folder mime
			folderCreate.Parents = []string{previus.Id}   // previus to folder to create

			// Create recursive folder
			for _, currentNode = range nodes[nodeIndex:] {
				folderCreate.Name = currentNode.Name // folder name
				if current, err = gdrive.driveService.Files.Create(&folderCreate).Fields("*").Do(); err != nil {
					return nil, err
				}
				gdrive.cachePut(currentNode.Path, current)
				folderCreate.Parents[0] = current.Id // Set new root
			}

			// return new folder
			return current, nil
		}
		gdrive.cachePut(currentNode.Path, current)
	}
	return current, nil
}

func (gdrive *Gdrive) MkdirAll(name string) (err error) {
	_, err = gdrive.mkdirAllNodes(name)
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

// Create file if not exists
func (gdrive *Gdrive) Create(name string) (file File, err error) {
	node := (*drive.File)(nil)
	if stat, err2 := gdrive.Stat(name); err2 == nil {
		node = stat.(*Stat).File
	} else if gdrive.checkMkdir(name) {
		pathNodes := gdrive.pathSplit(name)
		if node, err = gdrive.mkdirAllNodes(pathNodes[len(pathNodes)-2].Path); err != nil {
			return
		}
		file = &GdriveNode{filename: name, gClient: gdrive, nodeRoot: node, direction: DirectionWrite}
		return
	}

	if node == nil {
		file = &GdriveNode{filename: name, gClient: gdrive, node: nil, direction: DirectionWrite}
		return
	} else if node.MimeType == GoogleDriveMimeFolder {
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

	file = &GdriveNode{filename: name, gClient: gdrive, node: node, direction: DirectionWrite}
	return
}
