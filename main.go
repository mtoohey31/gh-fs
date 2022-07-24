package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"syscall"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	_ "bazil.org/fuse/fs/fstestutil"
	"github.com/alecthomas/kong"
	"github.com/cli/go-gh"
	"github.com/cli/go-gh/pkg/api"
)

// TODO: allow write access to the authenticated user's directory? This could be
// dangerous; it should probably be disabled by default with a flag to enable
// it. We can't provide write access to files inside repositories, but we could
// allow users to create/delete repos by creating/deleting directories in their
// user directory.

var cli struct {
	MountPoint string `arg:"" help:"Where the filesystem should be mounted." type:"existingdir"`
}

// TODO: does this have to be refreshed?
var client api.RESTClient

func main() {
	kong.Parse(&cli)

	var err error

	// TODO: investigate whether manually caching would be better, and how this
	// caching actually works, cause it might not be doing what we want it to
	client, err = gh.RESTClient(&api.ClientOptions{EnableCache: true})
	if err != nil {
		log.Fatalln(err)
	}

	c, err := fuse.Mount(
		cli.MountPoint,
		fuse.FSName("github"),
		fuse.Subtype("gh-fs"),
	)
	if err != nil {
		log.Fatalln(err)
	}
	defer c.Close()

	err = fs.Serve(c, FS{})
	if err != nil {
		log.Fatalln(err)
	}
}

// FS implements fs.FS. Permissions are set so only the user that this mount
// belongs to can do anything, so other users don't abuse the logged in user's
// api access.
type FS struct{}

func (FS) Root() (fs.Node, error) {
	return Root{}, nil
}

// Root implements fs.Node, fs.NodeStringLookuper, and HandleReadDirAller for
// the root of the filesystem, which contains users.
type Root struct{}

func (Root) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Inode = 0
	// Root can be read but not written
	a.Mode = os.ModeDir | 0o044
	return nil
}

func (Root) Lookup(ctx context.Context, name string) (fs.Node, error) {
	var res *User
	err := client.Get(fmt.Sprintf("users/%s", url.PathEscape(name)), &res)
	if err != nil {
		var apiErr api.HTTPError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			// User doesn't exist
			return nil, syscall.ENOENT
		}

		// TODO: detect and return syscall error codes for other equivalent
		// errors

		return nil, err
	}

	if res == nil {
		return nil, errors.New("TODO")
	}

	return res, nil
}

// Root conceptually contains all users, but we can't actually display that, so
// instead we display the users followed by the authenticated user, and the
// authenticated user themself. This means those will be the only visible
// folders, but all other users can still be accessed via looking.
func (Root) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	var res *User
	err := client.Get("user", &res)
	if err != nil {
		return nil, err
	}

	if res == nil {
		return nil, errors.New("TODO")
	}

	// TODO: include owners of repos the current user has starred too
	e := []fuse.Dirent{{Inode: res.Id, Type: fuse.DT_Dir, Name: res.Login}}
	v := url.Values{}
	v.Set("per_page", "100")

	for i := 1; true; i++ {
		var res []*User

		v.Set("page", strconv.Itoa(i))
		err := client.Get(fmt.Sprintf("user/following?%s", v.Encode()), &res)

		if err != nil {
			return nil, err
		}

		if len(res) == 0 {
			break
		}

		ne := make([]fuse.Dirent, len(res))
		for i, u := range res {
			ne[i] = fuse.Dirent{Inode: u.Id, Type: fuse.DT_Dir, Name: u.Login}
		}

		e = append(e, ne...)
	}

	return e, nil
}

// User implements fs.Node, fs.NodeStringLookuper, and HandleReadDirAller for
// a user directory, which contains the user's repositories.
type User struct {
	// Login is the user's github username, which is unqiue.
	Login string
	// Id is the user's unique github account id. This is used as the inode.
	Id uint64
}

func (u *User) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Inode = u.Id
	// User can be read but not written
	a.Mode = os.ModeDir | 0o044

	// TODO: set other equivalent information

	return nil
}

func (u *User) Lookup(ctx context.Context, name string) (fs.Node, error) {
	var res *Repo
	err := client.Get(fmt.Sprintf("repos/%s/%s", url.PathEscape(u.Login),
		url.PathEscape(name)), &res)
	if err != nil {
		var apiErr api.HTTPError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			// Repo doesn't exist
			return nil, syscall.ENOENT
		}
	}

	if res == nil {
		return nil, errors.New("TODO")
	}

	return res, nil
}

func (u *User) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	var e []fuse.Dirent
	v := url.Values{}
	v.Set("per_page", "100")

	for i := 1; true; i++ {
		var res []*Repo

		v.Set("page", strconv.Itoa(i))
		err := client.Get(fmt.Sprintf("users/%s/repos?%s",
			url.PathEscape(u.Login), v.Encode()), &res)

		if err != nil {
			return nil, err
		}

		if len(res) == 0 {
			break
		}

		ne := make([]fuse.Dirent, len(res))
		for i, r := range res {
			ne[i] = fuse.Dirent{
				Inode: r.Id,
				Type:  fuse.DT_Dir,
				Name:  r.Name,
			}
		}

		e = append(e, ne...)
	}

	return e, nil
}

// Repo implements fs.Node, fs.NodeStringLookuper, and HandleReadDirAller for
// a repository, which contains the entries at the root of that repository.
type Repo struct {
	// Name is the repository's name.
	Name string
	// FullName contains the owner and repository name, separated by a slash.
	FullName string `json:"full_name"`
	// TODO: handle repo and user inode collisions

	// Id is the repository's id. This is used as the inode.
	Id uint64
	// PushedAt is the time the repository was last pushed to. This is used as
	// the mtime.
	PushedAt time.Time `json:"pushed_at"`
	// UpdatedAt is the time the repository was last updated. This is used as
	// the ctime.
	UpdatedAt time.Time `json:"updated_at"`
}

func (r *Repo) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Inode = r.Id
	// Repo can be read but not written
	a.Mode = os.ModeDir | 0o044
	a.Mtime = r.PushedAt
	a.Ctime = r.UpdatedAt

	// TODO: set other equivalent information

	return nil
}

func (r *Repo) Lookup(ctx context.Context, name string) (fs.Node, error) {
	return (&Dir{Path: "", repo: r}).Lookup(ctx, name)
}

func (r *Repo) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	return (&Dir{Path: "", repo: r}).ReadDirAll(ctx)
}

// Content is the response to a github api repo/.../contents/... request.
type Content struct {
	// Type is the type of content this represents.
	Type string
	// Path is the relative path to this content from the repository root.
	Path string
	// Path is the basename of this content's path.
	Name string
}

func (c *Content) DirentType() fuse.DirentType {
	// TODO: handle submodules (which show up as "file" in the current version
	// of the api) and invalid types here
	switch c.Type {
	case "file":
		return fuse.DT_File
	case "dir":
		return fuse.DT_Dir
	case "symlink":
		return fuse.DT_Link
	default:
		return fuse.DT_Unknown
	}
}

// Dir implements fs.Node, fs.NodeStringLookuper, and HandleReadDirAller for
// a directory within a repository, which contains the entries within that
// directory.
type Dir struct {
	// Path is the relative path to this directory from the repository root.
	Path string
	// Repo is the repository that this directory belongs to.
	repo *Repo
}

func (d *Dir) Attr(ctx context.Context, a *fuse.Attr) error {
	// TODO: a.Inode =
	// Dir can be read but not written
	a.Mode = os.ModeDir | 0o044
	// TODO: determine the times for this specific sub-directory
	a.Mtime = d.repo.PushedAt
	a.Ctime = d.repo.UpdatedAt

	// TODO: set other equivalent information

	return nil
}

func (d *Dir) Lookup(ctx context.Context, name string) (fs.Node, error) {
	// TODO: handle empty d.Path here
	path := fmt.Sprintf("%s/%s", d.Path, name)

	var res interface{}
	err := client.Get(fmt.Sprintf("repos/%s/contents/%s", d.repo.FullName,
		path), &res)
	if err != nil {
		return nil, err
	}

	switch res.(type) {
	// Denotes a directory
	case []interface{}:
		return &Dir{Path: path, repo: d.repo}, nil

	// TODO: handle symlinks and submodules here
	default:
		// TODO: handle malformed responses here
		res := res.(map[string]interface{})
		content, err := base64.StdEncoding.DecodeString(res["content"].(string))
		if err != nil {
			return nil, err
		}

		return &File{Path: path, repo: d.repo, Content: content}, nil
	}
}

func (d *Dir) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	var res []*Content
	err := client.Get(fmt.Sprintf("repos/%s/contents/%s", d.repo.FullName,
		d.Path), &res)
	if err != nil {
		return nil, err
	}

	e := make([]fuse.Dirent, len(res))
	for i, c := range res {
		e[i] = fuse.Dirent{
			// TODO: Inode:
			Type: c.DirentType(),
			Name: c.Name,
		}
	}

	return e, nil
}

// File implements fs.Node and fs.HandleReadAller for a file within a
// repository.
type File struct {
	// Path is the relative path to this file from the repository root.
	Path string
	// Repo is the repository that this file belongs to.
	repo *Repo
	// Content is the base64 encoded contents of this file.
	Content []byte
}

func (f *File) Attr(ctx context.Context, a *fuse.Attr) error {
	// TODO: a.Inode =
	// File can be read but not written
	a.Mode = 0o044
	a.Size = uint64(len(f.Content))
	// TODO: determine the times for this specific file
	a.Mtime = f.repo.PushedAt
	a.Ctime = f.repo.UpdatedAt

	// TODO: set other equivalent information

	return nil
}

func (f *File) ReadAll(ctx context.Context) ([]byte, error) {
	return f.Content, nil
}
