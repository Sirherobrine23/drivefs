package drivefs

import (
	"io"
	"io/fs"
	"net/http"
	"time"

	"google.golang.org/api/drive/v3"
)

const (
	MimeFolder  string = "application/vnd.google-apps.folder"   // Folder mimetype
	MimeSyslink string = "application/vnd.google-apps.shortcut" // Syslink/hardlink
)

type DriveInfo struct {
	driveFile *drive.File
	modTime   time.Time
}

func fsStat(driveFile *drive.File) (fs.FileInfo, error) {
	var err error
	var modTime time.Time
	if modTime, err = time.Parse(time.RFC3339, driveFile.ModifiedTime); err != nil {
		return nil, err
	}
	return &DriveInfo{driveFile, modTime}, nil
}

func getNodeType(driveFile *drive.File) fs.FileMode {
	if driveFile.MimeType == MimeFolder {
		return fs.ModeDir
	} else if driveFile.MimeType == MimeSyslink {
		return fs.ModeSymlink
	}
	return fs.ModePerm
}

func (entry DriveInfo) MarshalText() ([]byte, error) { return []byte(entry.Name()), nil }
func (entry DriveInfo) Name() string                 { return entry.driveFile.Name }
func (entry DriveInfo) Size() int64                  { return entry.driveFile.Size }
func (entry DriveInfo) Mode() fs.FileMode            { return getNodeType(entry.driveFile) }
func (entry DriveInfo) ModTime() time.Time           { return entry.modTime }
func (entry DriveInfo) IsDir() bool                  { return entry.driveFile.MimeType == MimeFolder }
func (entry DriveInfo) Sys() any                     { return nil }

type DriveOpen struct {
	driveFile   *drive.File
	driveClient *Gdrive

	driveRead     *http.Response
	entrysEnabled bool
	entrys        []fs.DirEntry
}

func fsInfo(driveFile *drive.File, driveClient *Gdrive) fs.File {
	return &DriveOpen{driveFile, driveClient, nil, false, nil}
}

func (file DriveOpen) Stat() (fs.FileInfo, error) { return fsStat(file.driveFile) }

func (file *DriveOpen) ReadDir(n int) ([]fs.DirEntry, error) {
	if !file.entrysEnabled {
		enr, err := file.driveClient.listFiles(file.driveFile.Id)
		if err != nil {
			return nil, err
		}
		for _, gfile := range enr {
			fsStatt, err := fsStat(gfile)
			if err != nil {
				return nil, err
			}
			file.entrys = append(file.entrys, fs.FileInfoToDirEntry(fsStatt))
		}
	}

	if len(file.entrys) == 0 {
		return nil, io.EOF
	} else if n <= 0 {
		var def = file.entrys[:]
		file.entrys = []fs.DirEntry{}
		return def, nil
	}

	if len(file.entrys) <= n {
		n = len(file.entrys) - 1
	}
	var def = file.entrys[:n]
	file.entrys = file.entrys[n:]
	return def, nil
}

func (file *DriveOpen) Close() error {
	if file.driveRead == nil {
		return nil
	}
	return file.driveRead.Body.Close()
}

func (file *DriveOpen) Read(p []byte) (int, error) {
	if file.driveRead == nil {
		var err error
		if file.driveRead, err = file.driveClient.gDrive.Files.Get(file.driveFile.Id).Download(); err != nil {
			return 0, err
		}
	}
	return file.driveRead.Body.Read(p)
}
