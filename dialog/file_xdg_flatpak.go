//go:build flatpak && (linux || openbsd || freebsd || netbsd) && !android && !wasm && !js
// +build flatpak
// +build linux openbsd freebsd netbsd
// +build !android
// +build !wasm
// +build !js

package dialog

import (
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/storage"
	"github.com/rymdport/portal/filechooser"
)

func fileOpenOSOverride(d *FileDialog) bool {
	go func() {
		folderCallback, folder := d.callback.(func(fyne.ListableURI, error))
		options := &filechooser.OpenOptions{
			Modal:       true,
			Directory:   folder,
			AcceptLabel: d.confirmText,
		}
		if d.startingLocation != nil {
			options.Location = d.startingLocation.Path()
		}

		xid := d.parent.(interface{ GetX11ID() uint }).GetX11ID()
		parentWindow := "x11:" + strconv.FormatUint(uint64(xid), 16)

		if folder {
			uris, err := filechooser.OpenFile(parentWindow, "Open Folder", options)
			if err != nil {
				folderCallback(nil, err)
			}

			if len(uris) == 0 {
				folderCallback(nil, nil)
				return
			}

			uri, err := storage.ParseURI(uris[0])
			if err != nil {
				folderCallback(nil, err)
				return
			}

			folderCallback(storage.ListerForURI(uri))
			return
		}

		uris, err := filechooser.OpenFile(parentWindow, "Open File", options)
		fileCallback := d.callback.(func(fyne.URIReadCloser, error))
		if err != nil {
			fileCallback(nil, err)
			return
		}

		if len(uris) == 0 {
			fileCallback(nil, nil)
			return
		}

		uri, err := storage.ParseURI(uris[0])
		if err != nil {
			fileCallback(nil, err)
			return
		}

		fileCallback(storage.Reader(uri))
	}()
	return true
}

func fileSaveOSOverride(d *FileDialog) bool {
	go func() {
		options := &filechooser.SaveSingleOptions{
			Modal:       true,
			AcceptLabel: d.confirmText,
			FileName:    d.initialFileName,
		}
		if d.startingLocation != nil {
			options.Location = d.startingLocation.Path()
		}

		xid := d.parent.(interface{ GetX11ID() uint }).GetX11ID()
		parentWindow := "x11:" + strconv.FormatUint(uint64(xid), 16)

		callback := d.callback.(func(fyne.URIWriteCloser, error))
		uris, err := filechooser.SaveFile(parentWindow, "Open File", options)
		if err != nil {
			callback(nil, err)
			return
		}

		if len(uris) == 0 {
			callback(nil, nil)
			return
		}

		uri, err := storage.ParseURI(uris[0])
		if err != nil {
			callback(nil, err)
			return
		}

		callback(storage.Writer(uri))
	}()
	return true
}
