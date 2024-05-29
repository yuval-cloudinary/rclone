package cloudinary

import (
	"context"
	"fmt"
	"github.com/cloudinary/cloudinary-go/v2"
	"github.com/rclone/rclone/fs/config/configstruct"
	"io"
	"net/http"
	"time"

	"github.com/cloudinary/cloudinary-go/v2/api/admin"
	"github.com/cloudinary/cloudinary-go/v2/api/uploader"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/hash"
	"github.com/rclone/rclone/lib/pacer"
)

// Register with Fs
func init() {
	fs.Register(&fs.RegInfo{
		Name:        "cloudinary",
		Description: "Cloudinary",
		NewFs:       NewFs,
		Options: []fs.Option{
			{
				Name: "api_key",
				Help: "Cloudinary API Key",
			},
			{
				Name: "api_secret",
				Help: "Cloudinary API Secret",
			},
			{
				Name: "cloud_name",
				Help: "Cloudinary Cloud Name",
			},
		},
	})
}

// Options defines the configuration for this backend
type Options struct {
	APIKey    string `config:"api_key"`
	APISecret string `config:"api_secret"`
	CloudName string `config:"cloud_name"`
}

// Fs represents a remote cloudinary server
type Fs struct {
	name     string
	root     string
	opt      Options
	features *fs.Features
	pacer    *pacer.Pacer
	cld      *cloudinary.Cloudinary
}

// Object describes a cloudinary object
type Object struct {
	fs      *Fs
	remote  string
	size    int64
	modTime time.Time
	url     string
	md5sum  string
}

func (o *Object) Hash(ctx context.Context, ty hash.Type) (string, error) {
	if ty != hash.MD5 {
		return "", hash.ErrUnsupported
	}
	return o.md5sum, nil
}

func (o *Object) String() string {
	if o == nil {
		return "<nil>"
	}
	return o.remote
}

// NewFs constructs an Fs from the path, bucket:path
func NewFs(ctx context.Context, name string, root string, m configmap.Mapper) (fs.Fs, error) {
	opt := new(Options)
	err := configstruct.Set(m, opt)
	if err != nil {
		return nil, err
	}

	// Initialize the Cloudinary client
	cld, err := cloudinary.NewFromParams(opt.CloudName, opt.APIKey, opt.APISecret)
	if err != nil {
		return nil, fmt.Errorf("failed to create Cloudinary client: %w", err)
	}

	f := &Fs{
		name: name,
		root: root,
		opt:  *opt,
		cld:  cld,
	}

	f.features = (&fs.Features{
		CaseInsensitive:         false,
		CanHaveEmptyDirectories: true,
	}).Fill(ctx, f)

	return f, nil
}

// Name of the remote (as passed into NewFs)
func (f *Fs) Name() string {
	return f.name
}

// Root of the remote (as passed into NewFs)
func (f *Fs) Root() string {
	return f.root
}

// String converts this Fs to a string
func (f *Fs) String() string {
	return fmt.Sprintf("Cloudinary root '%s'", f.root)
}

// Features returns the optional features of this Fs
func (f *Fs) Features() *fs.Features {
	return f.features
}

// List the objects and directories in dir into entries.
func (f *Fs) List(ctx context.Context, dir string) (fs.DirEntries, error) {
	params := admin.AssetsParams{
		MaxResults: 100,
		Prefix:     f.root + dir,
	}
	assets, err := f.cld.Admin.Assets(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to list assets: %w", err)
	}

	var entries fs.DirEntries
	for _, asset := range assets.Assets {
		remote := asset.PublicID[len(f.root):]
		o := &Object{
			fs:      f,
			remote:  remote,
			size:    int64(asset.Bytes),
			modTime: asset.CreatedAt,
			url:     asset.URL,
		}
		entries = append(entries, o)
	}

	return entries, nil
}

// NewObject finds the Object at remote. If it can't be found it returns the error fs.ErrorObjectNotFound.
func (f *Fs) NewObject(ctx context.Context, remote string) (fs.Object, error) {
	// Note: Cloudinary doesn't provide a direct way to get a single asset by its public ID,
	// so we list assets with a limit of 1 to find the specific object.
	params := admin.AssetsParams{
		MaxResults: 1,
		Prefix:     f.root + remote,
	}
	assets, err := f.cld.Admin.Assets(ctx, params)
	if err != nil || len(assets.Assets) == 0 {
		return nil, fs.ErrorObjectNotFound
	}

	asset := assets.Assets[0]
	o := &Object{
		fs:      f,
		remote:  remote,
		size:    int64(asset.Bytes),
		modTime: asset.CreatedAt,
		url:     asset.URL,
	}

	return o, nil
}

// Put uploads content to Cloudinary
func (f *Fs) Put(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) (fs.Object, error) {
	params := uploader.UploadParams{
		PublicID: f.root + src.Remote(),
	}
	uploadResult, err := f.cld.Upload.Upload(ctx, in, params)
	if err != nil {
		return nil, fmt.Errorf("failed to upload to Cloudinary: %w", err)
	}

	o := &Object{
		fs:      f,
		remote:  src.Remote(),
		size:    src.Size(),
		modTime: time.Now(),
		url:     uploadResult.URL,
	}

	return o, nil
}

// Other required methods (not fully implemented):
func (f *Fs) Precision() time.Duration {
	return time.Millisecond
}

func (f *Fs) Hashes() hash.Set {
	return hash.Set(hash.None)
}

func (f *Fs) Mkdir(ctx context.Context, dir string) error {
	return nil
}

func (f *Fs) Rmdir(ctx context.Context, dir string) error {
	return nil
}

func (f *Fs) Remove(ctx context.Context, o fs.Object) error {
	params := uploader.DestroyParams{
		PublicID: f.root + o.Remote(),
	}
	_, err := f.cld.Upload.Destroy(ctx, params)
	return err
}

// Object methods
func (o *Object) Fs() fs.Info {
	return o.fs
}

func (o *Object) Remote() string {
	return o.remote
}

func (o *Object) ModTime(ctx context.Context) time.Time {
	return o.modTime
}

func (o *Object) Size() int64 {
	return o.size
}

func (o *Object) Storable() bool {
	return true
}

func (o *Object) SetModTime(ctx context.Context, modTime time.Time) error {
	return nil
}

func (o *Object) Open(ctx context.Context, options ...fs.OpenOption) (io.ReadCloser, error) {
	// Cloudinary assets can be accessed via URL directly
	resp, err := http.Get(o.url)
	if err != nil {
		return nil, fmt.Errorf("failed to open Cloudinary object: %w", err)
	}
	return resp.Body, nil
}

func (o *Object) Update(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) error {
	return nil
}

func (o *Object) Remove(ctx context.Context) error {
	return o.fs.Remove(ctx, o)
}
