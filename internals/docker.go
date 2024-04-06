package internals

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	dockerRegistryURL = "https://registry-1.docker.io/v2"
	dockerAuthURL     = "https://auth.docker.io/token"
	dockerAuthService = "registry.docker.io"
	grantType         = "password"
	libraryImage      = "library"
	manifestMediaType = "application/vnd.docker.distribution.manifest.v2+json"
)

type ImageConfig struct {
	Env        []string `json:"Env"`
	Cmd        []string `json:"Cmd"`
	WorkingDir string   `json:"WorkingDir"`
}

type ImageContainer struct {
	Config ImageConfig `json:"config"`
}

type PlatformDescription struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
}

type Manifest struct {
	Digest    string              `json:"digest"`
	MediaType string              `json:"mediaType"`
	Size      int                 `json:"size"`
	Platform  PlatformDescription `json:"platform,omitempty"`
}

type ImageManifestList struct {
	Manifests     []Manifest `json:"manifests"`
	MediaType     string     `json:"mediaType"`
	SchemaVersion int        `json:"schemaVersion"`
}

type LayerManifest struct {
	SchemaVersion int        `json:"schemaVersion"`
	MediaType     string     `json:"mediaType"`
	Config        Manifest   `json:"config"`
	Layers        []Manifest `json:"layers"`
}

type ImageDetail struct {
	base  string
	image string
	tag   string
}

type AuthResp struct {
	AccessToken string    `json:"access_token"`
	Scope       string    `json:"scope"`
	ExpiresIn   int       `json:"expires_in"`
	IssuedAt    time.Time `json:"issued_at"`
}

type DockerManager struct {
	clientId    string
	username    string
	password    string
	auth        *AuthResp
	imageDetail *ImageDetail
}

func NewDockerManager(clientId, username, password string) *DockerManager {
	return &DockerManager{
		clientId: clientId,
		username: username,
		password: password,
	}
}

func (dm *DockerManager) PullImage(imageName string) (string, *ImageConfig, error) {
	err := dm.parseImageDetail(imageName)

	if err != nil {
		return "", nil, err
	}

	err = dm.authenticate()

	if err != nil {
		return "", nil, err
	}

	manifest, err := dm.pullImageLayerManifest()

	if err != nil {
		return "", nil, err
	}

	layerPath, err := dm.fetchLayers(manifest)

	if err != nil {
		return "", nil, err
	}

	config, err := dm.FetchImageConfig(manifest)

	if err != nil {
		return "", nil, err
	}

	return layerPath, config, nil
}

func (dm *DockerManager) parseImageDetail(imageName string) error {
	if len(imageName) == 0 {
		return errors.New("image name is required")
	}

	imageDetail := &ImageDetail{}

	imageData := strings.Split(imageName, "/")

	if len(imageData) == 1 {
		name, tag := splitNameAndTag(imageData[0])

		imageDetail.base = libraryImage
		imageDetail.image = name
		imageDetail.tag = tag

		dm.imageDetail = imageDetail
		return nil
	}

	imageDetail.base = imageData[0]
	name, tag := splitNameAndTag(imageData[1])
	imageDetail.image = name
	imageDetail.tag = tag

	dm.imageDetail = imageDetail
	log.Println("Image details parsed successfully")
	return nil
}

func (dm *DockerManager) authenticate() error {
	if dm.username == "" || dm.password == "" {
		return errors.New("username and password is required to authenticate")
	}

	if dm.imageDetail == nil {
		return errors.New("image detail not found")
	}

	authResponse := &AuthResp{}

	form := url.Values{}
	form.Add("client_id", dm.clientId)
	form.Add("service", dockerAuthService)
	form.Add("grant_type", grantType)
	form.Add("username", dm.username)
	form.Add("password", dm.password)
	form.Add("scope", fmt.Sprintf("repository:%s/%s:pull", dm.imageDetail.base, dm.imageDetail.image))

	req, err := http.NewRequest(http.MethodPost, dockerAuthURL, strings.NewReader(form.Encode()))

	if err != nil {
		return err
	}

	req.Header = http.Header{}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)

	if err != nil {
		return err
	}

	data, err := io.ReadAll(resp.Body)

	if err != nil {
		return err
	}

	defer resp.Body.Close()

	err = json.Unmarshal(data, authResponse)

	if err != nil {
		return err
	}

	dm.auth = authResponse
	log.Println("Authentication completed")
	return nil
}

func (dm *DockerManager) pullImageLayerManifest() (*LayerManifest, error) {
	var err error

	base, imageName, tag, fetchHeader := dm.imageDetail.base, dm.imageDetail.image, dm.imageDetail.tag, manifestMediaType

	if base == libraryImage {
		tag, fetchHeader, err = dm.getShaTagForCurrentOS()

		if err != nil {
			return nil, err
		}
	}

	data, err := dm.fetchManifest(base, imageName, tag, fetchHeader)

	if err != nil {
		return nil, err
	}

	manifest := &LayerManifest{}

	err = json.Unmarshal(data, manifest)

	if err != nil {
		return nil, err
	}

	log.Printf("Manifest pulled successfully \n\n")
	log.Printf("Total Layers - %d \n", len(manifest.Layers))
	return manifest, nil
}

func (dm *DockerManager) getShaTagForCurrentOS() (string, string, error) {
	curOs, curArch := runtime.GOOS, runtime.GOARCH

	data, err := dm.fetchManifest(dm.imageDetail.base, dm.imageDetail.image, dm.imageDetail.tag, manifestMediaType)

	if err != nil {
		return "", "", err
	}

	manifestList := &ImageManifestList{}

	err = json.Unmarshal(data, manifestList)

	if err != nil {
		return "", "", err
	}

	for _, manifest := range manifestList.Manifests {
		if manifest.Platform.Architecture == curArch && manifest.Platform.OS == curOs {
			return manifest.Digest, manifest.MediaType, nil
		}
	}

	return dm.imageDetail.tag, manifestMediaType, nil
}

func (dm *DockerManager) fetchManifest(base, image, tag, acceptHeader string) ([]byte, error) {
	manifestURL := fmt.Sprintf("%s/%s/%s/manifests/%s", dockerRegistryURL, base, image, tag)

	req, err := http.NewRequest(http.MethodGet, manifestURL, nil)

	if err != nil {
		return nil, err
	}

	req.Header = http.Header{}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", dm.auth.AccessToken))
	req.Header.Set("Accept", acceptHeader)

	resp, err := http.DefaultClient.Do(req)

	if err != nil {
		return nil, err
	}

	data, err := io.ReadAll(resp.Body)
	resp.Body.Close()

	if err != nil {
		return nil, err
	}

	return data, nil
}

func (dm *DockerManager) fetchLayers(manifest *LayerManifest) (string, error) {
	extractDirectory := fmt.Sprintf("./images/%s", uuid.NewString())

	err := os.MkdirAll(extractDirectory, 0777)

	if err != nil {
		return "", err
	}

	for idx, layer := range manifest.Layers {
		data, err := dm.fetchLayerData(layer)

		if err != nil {
			return "", err
		}

		log.Printf("Fetched layer - %s (%d/%d) \n", layer.Digest, idx+1, len(manifest.Layers))

		layerReader := bytes.NewReader(data)

		zipped, err := gzip.NewReader(layerReader)

		if err != nil {
			return "", err
		}

		defer zipped.Close()

		err = extractLayer(extractDirectory, zipped)

		if err != nil {
			return "", err
		}

		log.Printf("Extracted layer - %s (%d/%d) \n", layer.Digest, idx+1, len(manifest.Layers))
	}

	log.Println("Layer fetching completed")
	return extractDirectory, nil
}

func (dm *DockerManager) fetchLayerData(layer Manifest) ([]byte, error) {
	layerUrl := fmt.Sprintf("%s/%s/%s/blobs/%s", dockerRegistryURL, dm.imageDetail.base, dm.imageDetail.image, layer.Digest)

	req, err := http.NewRequest(http.MethodGet, layerUrl, nil)

	if err != nil {
		return nil, err
	}

	req.Header = http.Header{}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", dm.auth.AccessToken))

	resp, err := http.DefaultClient.Do(req)

	if err != nil {
		return nil, err
	}

	data, err := io.ReadAll(resp.Body)
	defer resp.Body.Close()

	if err != nil {
		return nil, err
	}

	return data, nil
}

func (dm *DockerManager) FetchImageConfig(manifest *LayerManifest) (*ImageConfig, error) {
	configURL := fmt.Sprintf("%s/%s/%s/blobs/%s", dockerRegistryURL, dm.imageDetail.base, dm.imageDetail.image, manifest.Config.Digest)

	req, err := http.NewRequest(http.MethodGet, configURL, nil)

	if err != nil {
		return nil, err
	}

	req.Header = http.Header{}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", dm.auth.AccessToken))

	resp, err := http.DefaultClient.Do(req)

	if err != nil {
		return nil, err
	}

	data, err := io.ReadAll(resp.Body)
	resp.Body.Close()

	if err != nil {
		return nil, err
	}

	ImageContainer := &ImageContainer{}

	if err := json.Unmarshal(data, ImageContainer); err != nil {
		return nil, err
	}

	log.Println("Image config pulled")
	return &ImageContainer.Config, nil
}

func splitNameAndTag(name string) (string, string) {
	nameTagSplit := strings.Split(name, ":")

	if len(nameTagSplit) == 2 {
		return nameTagSplit[0], nameTagSplit[1]
	}

	return nameTagSplit[0], "latest"
}

func extractLayer(extractDirectory string, zipped *gzip.Reader) error {
	tr := tar.NewReader(zipped)

	for {
		header, err := tr.Next()

		if err == io.EOF {
			break
		}

		if err != nil {
			return err
		}

		filePath := filepath.Join(extractDirectory, header.Name)

		if header.Typeflag == tar.TypeDir {
			err := os.MkdirAll(filePath, 0777)

			if err != nil {
				return err
			}
		} else if header.Typeflag == tar.TypeReg || header.Typeflag == tar.TypeRegA {
			file, err := os.Create(filePath)

			if err != nil {
				return err
			}

			_, err = io.Copy(file, tr)

			if err != nil {
				return err
			}

			file.Close()

			if err := os.Chmod(filePath, 0777); err != nil {
				return err
			}
		}
	}

	return nil
}
