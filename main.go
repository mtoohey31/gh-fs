package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	_ "bazil.org/fuse/fs/fstestutil"
	"github.com/alecthomas/kong"
	"github.com/cli/go-gh"
	"github.com/cli/go-gh/pkg/api"
	graphql "github.com/cli/shurcooL-graphql"
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
var client api.GQLClient

func main() {
	kong.Parse(&cli)

	var err error

	// TODO: investigate whether manually caching would be better, and how this
	// caching actually works, cause it might not be doing what we want it to
	client, err = gh.GQLClient(&api.ClientOptions{EnableCache: true})
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
	var query struct {
		User *User `graphql:"user(login: $login)"`
	}
	err := client.Query("LookupUser", &query,
		map[string]interface{}{"login": graphql.String(name)})
	if err != nil {
		log.Println(err)
		return nil, err
	}

	return query.User, nil
}

type followingQuery struct {
	Edges []struct {
		Node struct {
			Login string
		}
	}
	PageInfo struct {
		EndCursor   string
		HasNextPage bool
	}
}

// Root conceptually contains all users, but we can't actually display that, so
// instead we display the users followed by the authenticated user, and the
// authenticated user themself. This means those will be the only visible
// folders, but all other users can still be accessed via lookup.
func (Root) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	// TODO: include owners of repos the current user has starred too

	var iq struct {
		Viewer struct {
			Login     string
			Following followingQuery `graphql:"following(first: 100)"`
		}
	}
	err := client.Query("GetViewerAndFollowing", &iq, nil)
	if err != nil {
		log.Println(err)
		return nil, err
	}

	e := make([]fuse.Dirent, len(iq.Viewer.Following.Edges)+1)
	e[0] = fuse.Dirent{
		// TODO: Inode: iq.Viewer.Id,
		Type: fuse.DT_Dir,
		Name: iq.Viewer.Login}
	for i, f := range iq.Viewer.Following.Edges {
		e[i+1] = fuse.Dirent{
			// TODO: Inode: iq.Viewer.Id,
			Type: fuse.DT_Dir,
			Name: f.Node.Login}
	}

	var sq struct {
		Viewer struct {
			Following followingQuery `graphql:"following(first: 100, after: $after)"`
		}
	}

	sq.Viewer.Following.PageInfo = iq.Viewer.Following.PageInfo

	for sq.Viewer.Following.PageInfo.HasNextPage {
		err := client.Query("GetFollowing", &sq, map[string]interface{}{
			"after": sq.Viewer.Following.PageInfo.EndCursor})

		if err != nil {
			log.Println(err)
			return nil, err
		}

		ne := make([]fuse.Dirent, len(sq.Viewer.Following.Edges))
		for i, f := range sq.Viewer.Following.Edges {
			ne[i] = fuse.Dirent{
				// TODO: Inode: f.Node.Id,
				Type: fuse.DT_Dir,
				Name: f.Node.Login}
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
}

func (u *User) Attr(ctx context.Context, a *fuse.Attr) error {
	// TODO: a.Inode =
	// User can be read but not written
	a.Mode = os.ModeDir | 0o044

	// TODO: set other equivalent information

	return nil
}

func (u *User) Lookup(ctx context.Context, name string) (fs.Node, error) {
	var query struct {
		Repository *Repo `graphql:"repository(owner: $owner, name: $name)"`
	}
	err := client.Query("LookupRepo", &query, map[string]interface{}{
		"owner": graphql.String(u.Login), "name": graphql.String(name)})
	if err != nil {
		log.Println(err)
		return nil, err
	}

	return query.Repository, nil
}

type repositoriesQuery struct {
	Edges []struct {
		Node struct {
			Name string
		}
	}
	PageInfo struct {
		EndCursor   string
		HasNextPage bool
	}
}

func (u *User) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	var iq struct {
		User struct {
			Repositories repositoriesQuery `graphql:"repositories(ownerAffiliations: OWNER, first: 100)"`
		} `graphql:"user(login: $login)"`
	}
	err := client.Query("GetUserRepositories", &iq, map[string]interface{}{
		"login": graphql.String(u.Login)})
	if err != nil {
		log.Println(err)
		return nil, err
	}

	e := make([]fuse.Dirent, len(iq.User.Repositories.Edges))
	for i, r := range iq.User.Repositories.Edges {
		e[i] = fuse.Dirent{
			// TODO: Inode: r.Node.Id,
			Type: fuse.DT_Dir,
			Name: r.Node.Name,
		}
	}

	var sq struct {
		User struct {
			Repositories repositoriesQuery `graphql:"repositories(ownerAffiliations: OWNER, first: 100, after: $after)"`
		} `graphql:"user(login: $login)"`
	}
	sq.User.Repositories = iq.User.Repositories

	for sq.User.Repositories.PageInfo.HasNextPage {
		err := client.Query("GetUserRepositories", &sq,
			map[string]interface{}{
				"after": graphql.String(sq.User.Repositories.PageInfo.EndCursor),
				"login": graphql.String(u.Login),
			})
		if err != nil {
			log.Println(err)
			return nil, err
		}

		ne := make([]fuse.Dirent, len(sq.User.Repositories.Edges))
		for i, r := range sq.User.Repositories.Edges {
			ne[i] = fuse.Dirent{
				// TODO: Inode: r.Node.Id,
				Type: fuse.DT_Dir,
				Name: r.Node.Name,
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
	// Owner is the owner of this repository.
	Owner struct{ Login string }
	// TODO: handle repo and user inode collisions

	// PushedAt is the time the repository was last pushed to. This is used as
	// the mtime.
	PushedAt time.Time
	// UpdatedAt is the time the repository was last updated. This is used as
	// the ctime.
	UpdatedAt time.Time

	// DefaultBranchRef is the mainb ranch for this repository.
	DefaultBranchRef struct{ Name string }
}

func (r *Repo) Attr(ctx context.Context, a *fuse.Attr) error {
	// TODO: a.Inode = r.Id
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
	path := filepath.Join(d.Path, name)
	// TODO: figure out how to differentiate between Tree and Blob without
	// asking for extraneous data
	var query struct {
		Repository struct {
			Object struct {
				Tree struct {
					AbbreviatedOid string
				} `graphql:"... on Tree"`
				Blob struct {
					Oid string
				} `graphql:"... on Blob"`
			} `graphql:"object(expression: $expression)"`
		} `graphql:"repository(name: $name, owner: $owner)"`
	}
	err := client.Query("StatDirEntry", &query, map[string]interface{}{
		"name":  graphql.String(d.repo.Name),
		"owner": graphql.String(d.repo.Owner.Login),
		"expression": graphql.String(fmt.Sprintf("%s:%s",
			d.repo.DefaultBranchRef.Name, path)),
	})
	if err != nil {
		log.Println(err)
		return nil, err
	}

	if query.Repository.Object.Tree.AbbreviatedOid != "" {
		return &Dir{Path: path, repo: d.repo}, nil
	} else if query.Repository.Object.Blob.Oid != "" {
		return &File{Path: path, repo: d.repo}, nil
	} else {
		return nil, syscall.ENOENT
	}
}

func (d *Dir) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	var query struct {
		Repository struct {
			Object struct {
				Tree struct {
					Entries []struct {
						Name string
						Type string
					}
				} `graphql:"... on Tree"`
			} `graphql:"object(expression: $expression)"`
		} `graphql:"repository(name: $name, owner: $owner)"`
	}
	err := client.Query("ListDir", &query, map[string]interface{}{
		"name":  graphql.String(d.repo.Name),
		"owner": graphql.String(d.repo.Owner.Login),
		"expression": graphql.String(fmt.Sprintf("%s:%s",
			d.repo.DefaultBranchRef.Name, d.Path)),
	})
	if err != nil {
		log.Println(err)
		return nil, err
	}

	e := make([]fuse.Dirent, len(query.Repository.Object.Tree.Entries))
	for i, entry := range query.Repository.Object.Tree.Entries {
		// TODO: figure out symlinks and submodules here
		var t fuse.DirentType
		switch entry.Type {
		case "blob":
			t = fuse.DT_File
		case "tree":
			t = fuse.DT_Dir
		}

		e[i] = fuse.Dirent{
			// TODO: Inode: 0,
			Type: t,
			Name: entry.Name,
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
}

func (f *File) Attr(ctx context.Context, a *fuse.Attr) error {
	var query struct {
		Repository struct {
			Object struct {
				Blob struct {
					ByteSize int
				} `graphql:"... on Blob"`
			} `graphql:"object(expression: $expression)"`
		} `graphql:"repository(name: $name, owner: $owner)"`
	}
	err := client.Query("GetFileByteSize", &query, map[string]interface{}{
		"name":  graphql.String(f.repo.Name),
		"owner": graphql.String(f.repo.Owner.Login),
		"expression": graphql.String(fmt.Sprintf("%s:%s",
			f.repo.DefaultBranchRef.Name, f.Path)),
	})
	if err != nil {
		log.Println(err)
		return err
	}
	log.Println(f)
	log.Println(query)

	// TODO: a.Inode =
	// File can be read but not written
	a.Mode = 0o044
	a.Size = uint64(query.Repository.Object.Blob.ByteSize)
	// TODO: determine the times for this specific file
	a.Mtime = f.repo.PushedAt
	a.Ctime = f.repo.UpdatedAt

	// TODO: set other equivalent information

	return nil
}

func (f *File) ReadAll(ctx context.Context) ([]byte, error) {
	// TODO: handle binary and truncated files
	var query struct {
		Repository struct {
			Object struct {
				Blob struct {
					Text string
				} `graphql:"... on Blob"`
			} `graphql:"object(expression: $expression)"`
		} `graphql:"repository(name: $name, owner: $owner)"`
	}
	err := client.Query("GetFileContents", &query, map[string]interface{}{
		"name":  graphql.String(f.repo.Name),
		"owner": graphql.String(f.repo.Owner.Login),
		"expression": graphql.String(fmt.Sprintf("%s:%s",
			f.repo.DefaultBranchRef.Name, f.Path)),
	})
	if err != nil {
		log.Println(err)
		return nil, err
	}

	return []byte(query.Repository.Object.Blob.Text), nil
}
