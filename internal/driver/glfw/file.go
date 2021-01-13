package glfw

import (
	"fyne.io/fyne"
	"fyne.io/fyne/storage"
)

// ListerForURI - deprecated in 2.0.0 - use storage.List() and storage.CanList() instead
func (d *gLDriver) ListerForURI(uri fyne.URI) (fyne.ListableURI, error) {
	return storage.ListerForURI(uri)
}

// FileReaderForURI - deprecated in 2.0.0 - use storage.Reader() instead
func (d *gLDriver) FileReaderForURI(uri fyne.URI) (fyne.URIReadCloser, error) {
	return storage.Reader(uri)
}

// FileWriterForURI - deprecated in 2.0.0 - use storage.Writer() instead
func (d *gLDriver) FileWriterForURI(uri fyne.URI) (fyne.URIWriteCloser, error) {
	return storage.Writer(uri)
}
