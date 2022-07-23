package main

import (
	"context"
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

// FS implements the github file system. Permissions are set so only the user
// that this mount belongs to can do anything, so other users don't abuse the
// logged in user's api access.
type FS struct{}

func (FS) Root() (fs.Node, error) {
	return Root{}, nil
}

// Root implements both Node and Handle for the root directory. Since it
// conceptually contains all users, it can't be read (since that would require
// listing all ~73 million users (at the time of writing)) and it can't be
// written because that would constitute creating a user.
type Root struct{}

func (Root) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Inode = 0
	// Root can't be read or written
	a.Mode = os.ModeDir | 0o004
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
			return []fuse.Dirent{}, err
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

// User implements both Node and Handle for a user directory.
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

// A user directory contains that user's repositories.
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
			return []fuse.Dirent{}, err
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

// Repo implements both Node and Handle for a repository directory.
type Repo struct {
	// TODO: handle repo and user inode collisions

	// Name is the repository's name.
	Name string
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
