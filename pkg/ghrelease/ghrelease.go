package ghrelease

import (
	"archive/tar"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/klauspost/compress/gzip"
	progressbar "github.com/schollz/progressbar/v3"
)

type ReleaseAsset struct {
	URL         string `json:"url"`
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
}

type Release struct {
	Id      int64          `json:"id"`
	TagName string         `json:"tag_name"`
	Assets  []ReleaseAsset `json:"assets"`
}

func Latest(org, repo string) (*Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", org, repo)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	var r Release

	err = json.NewDecoder(resp.Body).Decode(&r)
	if err != nil {
		return nil, err
	}

	return &r, nil
}

func ReadAsset(asset *ReleaseAsset) (io.ReadCloser, error) {
	req, err := http.NewRequest("GET", asset.URL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/octet-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	return resp.Body, nil
}

func UnpackAsset(asset *ReleaseAsset, dir string) error {
	pb := progressbar.NewOptions64(
		asset.Size,
		progressbar.OptionSetDescription(asset.Name),
		progressbar.OptionSetWriter(os.Stderr),
		progressbar.OptionShowBytes(true),
		progressbar.OptionSetWidth(10),
		progressbar.OptionThrottle(65*time.Millisecond),
		progressbar.OptionShowCount(),
		progressbar.OptionSpinnerType(14),
		progressbar.OptionFullWidth(),
	)

	pb.RenderBlank()
	defer pb.Clear()

	r, err := ReadAsset(asset)
	if err != nil {
		return err
	}

	defer r.Close()

	gr, err := gzip.NewReader(io.TeeReader(r, pb))
	if err != nil {
		return err
	}

	tr := tar.NewReader(gr)

	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}

		path := filepath.Join(dir, hdr.Name)

		fdir := filepath.Dir(path)

		if fdir != dir {
			os.MkdirAll(fdir, 0755)
		}

		f, err := os.Create(path)
		if err != nil {
			return err
		}

		io.Copy(f, tr)

		f.Chmod(fs.FileMode(hdr.Mode))

		f.Close()
	}

	return nil
}
