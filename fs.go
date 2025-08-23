package drivefs

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strconv"
	"syscall"

	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
	"sirherobrine23.com.br/Sirherobrine23/cgofuse/fuse/calls"
)

func httpRes(res *http.Response) *googleapi.ServerResponse {
	if res != nil {
		return &googleapi.ServerResponse{HTTPStatusCode: res.StatusCode, Header: res.Header.Clone()}
	}
	return nil
}

func (gdrive *Gdrive) Sub(dir string) (fs.FS, error) {
	nodeID, err := gdrive.getNode(dir)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: dir, Err: ProcessErr(fileRes(nodeID), err)}
	}
	return &Gdrive{
		GoogleConfig: gdrive.GoogleConfig,
		GoogleToken:  gdrive.GoogleToken,
		driveService: gdrive.driveService,
		cache:        gdrive.cache,
		cacheDir:     gdrive.cacheDir,
		rootDrive:    nodeID,
		SubDir:       path.Join(gdrive.SubDir),
	}, nil
}

func (gdrive *Gdrive) Statfs(_ string) (total, free uint64, err error) {
	info, err := gdrive.driveService.About.Get().Fields("*").Do()
	if err != nil {
		return 0, 0, err
	}
	return uint64(info.StorageQuota.Limit), uint64(info.StorageQuota.Limit - info.StorageQuota.Usage), nil
}

func (gdrive *Gdrive) Open(name string) (fs.File, error) {
	return gdrive.OpenFile(name, os.O_RDONLY, 0)
}
func (gdrive *Gdrive) Create(name string) (File, error) {
	return gdrive.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0666)
}
func (gdrive *Gdrive) ReadFile(name string) (b []byte, err error) {
	f, err := gdrive.Open(name)
	if err == nil {
		defer f.Close()
		return io.ReadAll(f)
	}
	return nil, err
}

func (gdrive *Gdrive) Lstat(name string) (fs.FileInfo, error) {
	fileNode, err := gdrive.getNode(name)
	if err != nil {
		return nil, err
	}
	return &NodeStat{File: fileNode}, nil
}

// Resolve path and return File or Folder Stat
func (gdrive *Gdrive) Stat(name string) (fs.FileInfo, error) {
	name = pathManipulate(name).CleanPath()
	fileNode, err := gdrive.getNode(name)
	if err != nil {
		return nil, err
	}

	// Loop to check if is shortcut
	for limit := 200_000; limit > 0 && fileNode.MimeType == GoogleDriveMimeSyslink; limit-- {
		if fileNode, err = gdrive.driveService.Files.Get(fileNode.ShortcutDetails.TargetId).Do(); err != nil {
			return nil, ProcessErr(nil, err)
		}
	}

	return &NodeStat{File: fileNode}, nil
}

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

	return gdrive.forwardPathResolve(fileNode.Id)
}

func fileRes(res *drive.File) *googleapi.ServerResponse {
	if res != nil {
		return &res.ServerResponse
	}
	return nil
}

func (gdrive *Gdrive) ReadDir(name string) ([]fs.DirEntry, error) {
	name = pathManipulate(name).CleanPath()

	node, err := gdrive.getNode(name)
	if err != nil {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: ProcessErr(fileRes(node), err)}
	}

	files, err := gdrive.filesFromNode(node.Id)
	if err != nil {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: ProcessErr(nil, err)}
	}

	if gdrive.cacheDir != nil {
		gdrive.cacheDir.Set(DefaultCacheTime, path.Join(gdrive.SubDir, name), files)
		gdrive.cacheDir.Set(DefaultCacheTime, node.Id, files)
	}
	if gdrive.cache != nil {
		for _, file := range files {
			gdrive.cache.Set(DefaultCacheTime, path.Join(gdrive.SubDir, name, file.Name), file)
		}
	}

	return convertDriveToDir(files), nil
}

func (gdrive *Gdrive) Mkdir(name string, perm fs.FileMode) (err error) {
	name = pathManipulate(name).CleanPath()
	if _, err = gdrive.getNode(name); err == nil {
		return &fs.PathError{Op: "mkdir", Path: name, Err: fs.ErrExist} // return exist path
	}

	rootNode, err := gdrive.getNode(path.Dir(name))
	if err != nil {
		return &fs.PathError{Op: "mkdir", Path: name, Err: err}
	}

	node, err := gdrive.driveService.Files.Create(&drive.File{
		Name:       name,
		MimeType:   GoogleDriveMimeFolder,
		Parents:    []string{rootNode.Id},
		Properties: map[string]string{UnixModeProperties: strconv.Itoa(int(fs.ModeDir | perm))},
	}).Fields("*").Do()
	if err != nil {
		err = ProcessErr(fileRes(node), err)
	} else if gdrive.cache != nil {
		gdrive.cache.Set(DefaultCacheTime, path.Join(gdrive.SubDir, name), node)
	}

	return
}

func (gdrive *Gdrive) Remove(name string) error {
	name = pathManipulate(name).CleanPath()

	node, err := gdrive.getNode(name)
	if err != nil {
		return &fs.PathError{Op: "mkdir", Path: name, Err: ProcessErr(nil, err)}
	}

	if err = gdrive.driveService.Files.Delete(node.Id).Do(); err != nil {
		return &fs.PathError{Op: "mkdir", Path: name, Err: ProcessErr(nil, err)}
	}

	if gdrive.cache != nil {
		gdrive.cache.Delete(name)
	}
	if gdrive.cacheDir != nil {
		gdrive.cacheDir.Delete(name)
		gdrive.cacheDir.Delete(node.Id)
	}

	return nil
}

func (gdrive *Gdrive) Rename(oldName, newName string) error {
	oldNode, err := gdrive.getNode(oldName)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: oldName, New: newName, Err: ProcessErr(fileRes(oldNode), err)}
	}

	if path.Dir(oldName) == path.Dir(newName) {
		res, err := gdrive.driveService.Files.Update(oldNode.Id, &drive.File{Name: path.Base(newName)}).Fields("*").Do()
		if err != nil {
			return &os.LinkError{Op: "rename", Old: oldName, New: newName, Err: ProcessErr(fileRes(res), err)}
		} else if gdrive.cache != nil {
			gdrive.cache.Delete(oldName)
			gdrive.cache.Set(DefaultCacheTime, path.Join(gdrive.SubDir, newName), res)
		}
		return nil
	}

	newRootNode, err := gdrive.getNode(path.Dir(newName))
	if err != nil {
		return &os.LinkError{Op: "rename", Old: oldName, New: newName, Err: ProcessErr(fileRes(newRootNode), err)}
	}
	oldRootNode, err := gdrive.getNode(path.Dir(oldName))
	if err != nil {
		return &os.LinkError{Op: "rename", Old: oldName, New: newName, Err: ProcessErr(fileRes(newRootNode), err)}
	}

	updateParent := gdrive.driveService.Files.Update(oldNode.Id, &drive.File{Name: path.Base(newName)}).Fields("*")
	updateParent.RemoveParents(oldRootNode.Id).AddParents(newRootNode.Id)

	res, err := updateParent.Do()
	if err != nil {
		return &os.LinkError{Op: "rename", Old: oldName, New: newName, Err: ProcessErr(fileRes(res), err)}
	} else if gdrive.cache != nil {
		gdrive.cache.Delete(oldName)
		gdrive.cache.Set(DefaultCacheTime, path.Join(gdrive.SubDir, newName), res)
	}

	return nil
}

func (gdrive *Gdrive) OpenFile(name string, flag int, perm fs.FileMode) (_ File, err error) {
	name = pathManipulate(name).CleanPath()

	// Ignore Read+Write open
	if calls.OpenFlags(flag).Includes(os.O_RDWR) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}

	var driveNode *drive.File
	if driveNode, err = gdrive.getNode(name); err != nil {
		if errors.Is(err, fs.ErrNotExist) && !calls.OpenFlags(flag).Includes(flag, os.O_CREATE) {
			err = ProcessErr(fileRes(driveNode), err)
			return
		}

		parentRoot, err := gdrive.getNode(path.Dir(name))
		if err != nil {
			return nil, &fs.PathError{Op: "open", Path: name, Err: ProcessErr(fileRes(driveNode), err)}
		}

		var fileMake drive.File
		fileMake.Parents = []string{parentRoot.Id}
		fileMake.Name = path.Base(name)

		if calls.OpenFlags(flag).Includes(flag, syscall.S_IFDIR, syscall.S_IFDIR, int(fs.ModeDir)) {
			fileMake.MimeType = GoogleDriveMimeFolder
		} else {
			fileMake.MimeType = GoogleDriveMimeFile
		}

		driveNode, err = gdrive.driveService.Files.Create(&fileMake).Fields("*").Do()
		if err != nil {
			return nil, &fs.PathError{Op: "open", Path: name, Err: ProcessErr(fileRes(driveNode), err)}
		}
	}

	if driveNode == nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}

	if driveNode.MimeType == GoogleDriveMimeFolder {
		fileList, err := gdrive.filesFromNode(driveNode.Id)
		if err != nil {
			return nil, &fs.PathError{Op: "open", Path: name, Err: ProcessErr(fileRes(driveNode), err)}
		}

		return &DirNode{
			Node:   driveNode,
			Offset: 0,
			Files:  fileList,
		}, nil
	}

	fipe := &FileNode{
		Client: gdrive,
		Node:   driveNode,
		Offset: 0,
	}

	if calls.OpenFlags(flag).Includes(syscall.O_RDWR, syscall.O_WRONLY, syscall.O_CREAT, syscall.O_TRUNC) {
		fipe.Reader, fipe.Writer = io.Pipe()
	} else {
		res, err := openFileAPI(gdrive.driveService.Files.Get(driveNode.Id))
		if err != nil {
			return nil, &fs.PathError{Op: "open", Path: name, Err: ProcessErr(fileRes(driveNode), err)}
		}
		fipe.Reader = res.Body
	}

	return fipe, nil
}
