package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
)

type Client struct {
	socketPath string
}

type EventHandler func(Event)

func NewClient() (*Client, error) {
	path, err := SocketPath()
	if err != nil {
		return nil, err
	}
	return &Client{socketPath: path}, nil
}

func (c *Client) Status() (Status, error) {
	var status Status
	if err := c.call(CommandStatus, nil, &status); err != nil {
		return Status{}, err
	}
	return status, nil
}

func (c *Client) Shutdown() error {
	return c.call(CommandShutdown, nil, nil)
}

func (c *Client) ListDrives() (DriveListResponse, error) {
	var out DriveListResponse
	if err := c.call(CommandDriveList, nil, &out); err != nil {
		return DriveListResponse{}, err
	}
	return out, nil
}

func (c *Client) UseDrive(selector string) (DriveUseResponse, error) {
	var out DriveUseResponse
	if err := c.call(CommandDriveUse, DriveUseRequest{Selector: selector}, &out); err != nil {
		return DriveUseResponse{}, err
	}
	return out, nil
}

func (c *Client) PWD() (PathResponse, error) {
	var out PathResponse
	if err := c.call(CommandPWD, nil, &out); err != nil {
		return PathResponse{}, err
	}
	return out, nil
}

func (c *Client) CD(path string) (PathResponse, error) {
	var out PathResponse
	if err := c.call(CommandCD, PathRequest{Path: path}, &out); err != nil {
		return PathResponse{}, err
	}
	return out, nil
}

func (c *Client) List(path string) (ListResponse, error) {
	var out ListResponse
	if err := c.call(CommandList, PathRequest{Path: path}, &out); err != nil {
		return ListResponse{}, err
	}
	return out, nil
}

func (c *Client) Find(query string, limit int) (FindResponse, error) {
	var out FindResponse
	if err := c.call(CommandFind, FindRequest{Query: query, Limit: limit}, &out); err != nil {
		return FindResponse{}, err
	}
	return out, nil
}

func (c *Client) Mkdir(path string, parents bool) (EntryResponse, error) {
	var out EntryResponse
	if err := c.call(CommandMkdir, MkdirRequest{Path: path, Parents: parents}, &out); err != nil {
		return EntryResponse{}, err
	}
	return out, nil
}

func (c *Client) Remove(path string, recursive bool) (EntryResponse, error) {
	var out EntryResponse
	if err := c.call(CommandRemove, RemoveRequest{Path: path, Recursive: recursive}, &out); err != nil {
		return EntryResponse{}, err
	}
	return out, nil
}

func (c *Client) Move(source string, destination string) (EntryResponse, error) {
	var out EntryResponse
	if err := c.call(CommandMove, MoveRequest{Source: source, Destination: destination}, &out); err != nil {
		return EntryResponse{}, err
	}
	return out, nil
}

func (c *Client) VaultStatus() (VaultResponse, error) {
	var out VaultResponse
	if err := c.call(CommandVaultStatus, nil, &out); err != nil {
		return VaultResponse{}, err
	}
	return out, nil
}

func (c *Client) VaultUnlock(password string) (VaultResponse, error) {
	var out VaultResponse
	if err := c.call(CommandVaultUnlock, VaultUnlockRequest{Password: password}, &out); err != nil {
		return VaultResponse{}, err
	}
	return out, nil
}

func (c *Client) VaultLock() (VaultResponse, error) {
	var out VaultResponse
	if err := c.call(CommandVaultLock, nil, &out); err != nil {
		return VaultResponse{}, err
	}
	return out, nil
}

func (c *Client) Upload(localPath string, remotePath string, encrypt bool, onEvent EventHandler) (UploadResponse, error) {
	var out UploadResponse
	err := c.stream(CommandUpload, UploadRequest{
		LocalPath:  localPath,
		RemotePath: remotePath,
		Encrypt:    encrypt,
	}, &out, onEvent)
	if err != nil {
		return UploadResponse{}, err
	}
	return out, nil
}

func (c *Client) Download(remotePath string, localPath string, onEvent EventHandler) (DownloadResponse, error) {
	var out DownloadResponse
	err := c.stream(CommandDownload, DownloadRequest{
		RemotePath: remotePath,
		LocalPath:  localPath,
	}, &out, onEvent)
	if err != nil {
		return DownloadResponse{}, err
	}
	return out, nil
}

func (c *Client) call(command string, payload any, out any) error {
	req, err := NewRequest(command, payload)
	if err != nil {
		return err
	}

	conn, err := dialSocket(c.socketPath)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(bufio.NewReader(conn))

	if err := enc.Encode(req); err != nil {
		return fmt.Errorf("daemon request: %w", err)
	}
	var frame Frame
	if err := dec.Decode(&frame); err != nil {
		return fmt.Errorf("daemon response: %w", err)
	}
	if !frame.OK {
		if frame.Error == "" {
			frame.Error = "daemon request failed"
		}
		return fmt.Errorf("%s", frame.Error)
	}
	if out != nil && len(frame.Payload) > 0 {
		if err := json.Unmarshal(frame.Payload, out); err != nil {
			return fmt.Errorf("daemon response payload: %w", err)
		}
	}
	return nil
}

func (c *Client) stream(command string, payload any, out any, onEvent EventHandler) error {
	req, err := NewRequest(command, payload)
	if err != nil {
		return err
	}

	conn, err := dialSocket(c.socketPath)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(bufio.NewReader(conn))

	if err := enc.Encode(req); err != nil {
		return fmt.Errorf("daemon request: %w", err)
	}
	for {
		var frame Frame
		if err := dec.Decode(&frame); err != nil {
			return fmt.Errorf("daemon response: %w", err)
		}
		switch frame.Type {
		case "event":
			if onEvent == nil {
				continue
			}
			var event Event
			if len(frame.Payload) > 0 {
				if err := json.Unmarshal(frame.Payload, &event); err != nil {
					return fmt.Errorf("daemon event payload: %w", err)
				}
			}
			if event.Name == "" {
				event.Name = frame.Event
			}
			onEvent(event)
		case "response", "":
			if !frame.OK {
				if frame.Error == "" {
					frame.Error = "daemon request failed"
				}
				return fmt.Errorf("%s", frame.Error)
			}
			if out != nil && len(frame.Payload) > 0 {
				if err := json.Unmarshal(frame.Payload, out); err != nil {
					return fmt.Errorf("daemon response payload: %w", err)
				}
			}
			return nil
		default:
			return fmt.Errorf("unexpected daemon frame type %q", frame.Type)
		}
	}
}
