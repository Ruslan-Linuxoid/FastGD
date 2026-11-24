package drive

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

const MimeFolder = "application/vnd.google-apps.folder"

const listPageSize = 1000

const fileFields = "id,name,mimeType,parents,driveId,sharedWithMeTime,trashed,explicitlyTrashed,size,md5Checksum," +
	"headRevisionId,version,modifiedTime,createdTime,shortcutDetails(targetId)," +
	"capabilities(" + rightsAsked + ")"

const rightsAsked = "canModifyContent,canTrash,canRename,canAddChildren,canRemoveChildren,canDownload," +
	"canRemoveMyDriveParent,canAddMyDriveParent,canReadRevisions,canMoveItemWithinDrive,canMoveItemOutOfDrive"

func FieldsMark() string {
	sum := sha256.Sum256([]byte(fileFields))
	return hex.EncodeToString(sum[:8])
}

type File struct {
	ID       string
	Name     string
	MimeType string
	Parents  []string

	DriveID string

	Shared  bool
	Trashed bool

	OwnTrashed bool
	Size       int64
	MD5        string

	HeadRevision string
	Version      int64
	Modified     time.Time
	Created      time.Time

	ShortcutTarget string

	ViewLink string

	Can Rights
}

type Rights struct {
	ModifyContent, Trash, Rename, Move, AddChildren, RemoveChildren, Download, OutOfDrive bool
}

type Options struct {
	BaseURL string

	HTTPClient *http.Client

	Limits Limits

	Speed Speed

	Log *slog.Logger
}

type Client struct {
	svc  *drive.Service
	pace *pacer

	http       *http.Client
	uploadBase string

	units atomic.Int64
	bytes atomic.Int64

	speed    Speed
	down, up *rate.Limiter

	changesPage *pageSize
}

func (c *Client) Speed() Speed { return c.speed }

const googleBase = "https://www.googleapis.com"

func New(ctx context.Context, o Options) (*Client, error) {
	if o.HTTPClient == nil {
		return nil, errors.New("drive: no HTTP client")
	}

	hc := *o.HTTPClient
	if hc.Transport == nil {
		hc.Transport = http.DefaultTransport
	}
	hc.Transport = rightsGiven{hc.Transport}
	o.HTTPClient = &hc
	opts := []option.ClientOption{option.WithHTTPClient(o.HTTPClient)}
	base := googleBase
	if o.BaseURL != "" {
		base = strings.TrimSuffix(o.BaseURL, "/")
		opts = append(opts, option.WithEndpoint(base+"/drive/v3/"))
	}
	svc, err := drive.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("drive: %w", err)
	}
	c := &Client{
		svc: svc, pace: newPacer(o.Limits), http: o.HTTPClient, uploadBase: base,
		speed: o.Speed, down: newThrottle(o.Speed.Download), up: newThrottle(o.Speed.Upload),
		changesPage: newPageSize(cmp.Or(o.Log, slog.New(slog.DiscardHandler))),
	}
	hc.CheckRedirect = c.redirectsToDrive(hc.CheckRedirect)
	return c, nil
}

func (c *Client) RootID(ctx context.Context) (string, error) {
	f, err := get(ctx, c, costOther, func() (*drive.File, error) {
		return c.svc.Files.Get("root").Fields("id").Context(ctx).Do()
	})
	if err != nil {
		return "", fmt.Errorf("drive: root: %w", err)
	}
	return f.Id, nil
}

var ErrNotFound = errors.New("drive: no such item")

func (c *Client) Get(ctx context.Context, id string) (*File, error) {
	f, err := get(ctx, c, costOther, func() (*drive.File, error) {
		return c.svc.Files.Get(id).Fields(fileFields + ",webViewLink").SupportsAllDrives(true).Context(ctx).Do()
	})
	if hasCode(err, http.StatusNotFound) {
		return nil, fmt.Errorf("drive: get %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("drive: get %s: %w", id, err)
	}
	out, err := convert(f)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) StartPageToken(ctx context.Context) (string, error) {
	t, err := get(ctx, c, costOther, func() (*drive.StartPageToken, error) {
		return c.svc.Changes.GetStartPageToken().SupportsAllDrives(true).Context(ctx).Do()
	})
	if err != nil {
		return "", fmt.Errorf("drive: start page token: %w", err)
	}
	return t.StartPageToken, nil
}

func (c *Client) ListAll(ctx context.Context, fn func([]File) error) error {
	token := ""
	for {
		call := c.svc.Files.List().
			Q("trashed = false").
			Corpora("allDrives").
			IncludeItemsFromAllDrives(true).
			SupportsAllDrives(true).
			PageSize(listPageSize).
			Fields("nextPageToken,files(" + fileFields + ")").
			Context(ctx)
		if token != "" {
			call = call.PageToken(token)
		}
		page, err := get(ctx, c, costList, func() (*drive.FileList, error) {
			return call.Do()
		})
		if err != nil {
			return fmt.Errorf("drive: list: %w", err)
		}
		files := make([]File, 0, len(page.Files))
		for _, f := range page.Files {
			cf, err := convert(f)
			if err != nil {
				return err
			}
			files = append(files, cf)
		}
		if err := fn(files); err != nil {
			return err
		}
		if page.NextPageToken == "" {
			return nil
		}
		token = page.NextPageToken
	}
}
