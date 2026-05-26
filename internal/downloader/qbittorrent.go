package downloader

import (
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/luizg/jackui/internal/config"
)

type QBittorrent struct {
	name     string
	baseURL  string
	username string
	password string
	client   *http.Client
	loggedIn bool
}

func NewQBittorrent(dc config.DownloadClient) *QBittorrent {
	jar, _ := cookiejar.New(nil)
	return &QBittorrent{
		name:     dc.Name,
		baseURL:  strings.TrimRight(dc.URL, "/"),
		username: dc.Username,
		password: dc.Password,
		client: &http.Client{
			Timeout: 15 * time.Second,
			Jar:     jar,
		},
	}
}

func (q *QBittorrent) Name() string { return q.name }
func (q *QBittorrent) Type() string { return "qbittorrent" }

func (q *QBittorrent) login() error {
	loginURL := fmt.Sprintf("%s/api/v2/auth/login", q.baseURL)

	form := url.Values{}
	form.Set("username", q.username)
	form.Set("password", q.password)

	resp, err := q.client.PostForm(loginURL, form)
	if err != nil {
		return fmt.Errorf("login request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	bodyStr := strings.TrimSpace(string(body))

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("login failed with status %d", resp.StatusCode)
	}

	if bodyStr == "Fails." {
		return fmt.Errorf("login failed: invalid credentials")
	}

	q.loggedIn = true
	return nil
}

func (q *QBittorrent) ensureLoggedIn() error {
	if !q.loggedIn {
		return q.login()
	}
	return nil
}

func (q *QBittorrent) AddMagnet(magnetURI string, savePath string) error {
	if err := q.ensureLoggedIn(); err != nil {
		return err
	}

	addURL := fmt.Sprintf("%s/api/v2/torrents/add", q.baseURL)

	form := url.Values{}
	form.Set("urls", magnetURI)
	if savePath != "" {
		form.Set("savepath", savePath)
	}

	resp, err := q.client.PostForm(addURL, form)
	if err != nil {
		// Try re-login once
		q.loggedIn = false
		if loginErr := q.login(); loginErr != nil {
			return fmt.Errorf("failed to add magnet: %w", err)
		}
		resp, err = q.client.PostForm(addURL, form)
		if err != nil {
			return fmt.Errorf("failed to add magnet after re-login: %w", err)
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("add magnet failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

func (q *QBittorrent) AddTorrentURL(torrentURL string, savePath string) error {
	if err := q.ensureLoggedIn(); err != nil {
		return err
	}

	addURL := fmt.Sprintf("%s/api/v2/torrents/add", q.baseURL)

	form := url.Values{}
	form.Set("urls", torrentURL)
	if savePath != "" {
		form.Set("savepath", savePath)
	}

	resp, err := q.client.PostForm(addURL, form)
	if err != nil {
		q.loggedIn = false
		if loginErr := q.login(); loginErr != nil {
			return fmt.Errorf("failed to add torrent URL: %w", err)
		}
		resp, err = q.client.PostForm(addURL, form)
		if err != nil {
			return fmt.Errorf("failed to add torrent URL after re-login: %w", err)
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("add torrent URL failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}
