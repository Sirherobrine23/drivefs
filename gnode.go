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

	driveRead *http.Response
	offset    int64 // current read offset

	entrysEnabled bool
	entrys        []fs.DirEntry
	entrysN       int
}

func fsInfo(driveFile *drive.File, driveClient *Gdrive) fs.File {
	return &DriveOpen{driveFile, driveClient, nil, 0, false, nil, 0}
}

func (file DriveOpen) Stat() (fs.FileInfo, error) { return fsStat(file.driveFile) }

func (file *DriveOpen) ReadDir(n int) (_ []fs.DirEntry, err error) {
	if file.driveFile.MimeType != MimeFolder {
		return nil, fs.ErrInvalid
	} else if n <= 0 {
		file.entrysEnabled = true
		return file.driveClient.nReadDir(file.driveFile.Id)
	}

	if !file.entrysEnabled {
		file.entrysEnabled = true
		if file.entrys, err = file.driveClient.nReadDir(file.driveFile.Id); err != nil {
			return nil, err
		}
	}

	if len(file.entrys) >= file.entrysN {
		file.entrysEnabled = false
		return []fs.DirEntry{}, io.EOF
	} else if len(file.entrys)-file.entrysN > 0 && len(file.entrys)-file.entrysN < n {
	}

	defer func() { file.entrysN += n }()
	return file.entrys[file.entrysN:n], nil
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
	file.offset += int64(len(p))
	return file.driveRead.Body.Read(p)
}

func (file *DriveOpen) Seek(offset int64, whence int) (int64, error) {
	if file.driveRead != nil {
		switch whence {
		case 0:
			file.driveRead.Body.Close()
			var err error
			if file.driveRead, err = file.driveClient.gDrive.Files.Get(file.driveFile.Id).Download(); err != nil {
				return 0, err
			} else if n, err := io.CopyN(io.Discard, file.driveRead.Body, offset); err != nil {
				return n, err
			}
		case 1:
			if n, err := io.CopyN(io.Discard, file.driveRead.Body, offset); err != nil {
				return n, err
			}
			offset += file.offset
		case 2:
			offset += file.driveFile.Size
		}
		if offset < 0 || offset > file.driveFile.Size {
			return 0, &fs.PathError{Op: "seek", Path: file.driveFile.Id, Err: fs.ErrInvalid}
		}
		file.offset = offset
		return offset, nil
	}
	return 0, &fs.PathError{Op: "seek", Path: file.driveFile.Id, Err: fs.ErrInvalid}
}
