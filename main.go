package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/bitrise-io/go-utils/log"
	"io/ioutil"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type configsModel struct {
	stfHostURL        string
	stfAccessToken    string
	deviceFilter      string
	deviceNumberLimit int
	adbKeyPub         string
	adbKey            string
}

//Device ...
type Device struct {
	Serial string `json:"serial"`
}

//RemoteConnection ...
type RemoteConnection struct {
	RemoteConnectURL string `json:"remoteConnectUrl"`
}

const devicesEndpoint = "/api/v1/devices"
const userDevicesEndpoint = "/api/v1/user/devices"

var random = rand.New(rand.NewSource(time.Now().UnixNano()))

var client = &http.Client{Timeout: time.Second * 10}

func main() {
	configs := createConfigsModelFromEnvs()
	configs.dump()
	if err := configs.validate(); err != nil {
		log.Errorf("Could not validate config, error: %s", err)
		os.Exit(1)
	}

	serials, err := getSerials(configs)
	if err != nil {
		log.Errorf("Could not get device serials, error: %s", err)
		os.Exit(2)
	}
	homeDir, err := getHomeDir()
	if err != nil {
		log.Errorf("Could not determine current user home directory, error: %s", err)
		os.Exit(3)
	}

	if err := setAdbKeys(configs, homeDir); err != nil {
		log.Errorf("Could not set ADB keys, error: %s", err)
		os.Exit(4)
	}

	deviceCount := calculateDeviceCount(configs, serials)
	connectedDeviceSerials := []string{}

	connectedDeviceCount := 0
	for _, serial := range serials {
		if err := connectDeviceToADB(configs, serial); err != nil {
			log.Warnf("Device %s ignored, error: %s", serial, err)
		} else {
			connectedDeviceCount++
			connectedDeviceSerials = append(connectedDeviceSerials, serial)
		}
		if connectedDeviceCount >= deviceCount {
			break
		}
	}

	if err := exportArrayWithEnvman("STF_DEVICE_SERIAL_LIST", connectedDeviceSerials); err != nil {
		log.Errorf("Could export device serials with envman, error: %s", err)
		os.Exit(5)
	}
	if connectedDeviceCount == 0 {
		log.Errorf("No devices can be connected to ADB")
		os.Exit(6)
	}
}

func calculateDeviceCount(configs configsModel, serials []string) int {
	if configs.deviceNumberLimit > 0 && configs.deviceNumberLimit < len(serials) {
		return configs.deviceNumberLimit
	}
	return len(serials)
}

func connectDeviceToADB(configs configsModel, serial string) error {
	if err := addDeviceUnderControl(configs, serial); err != nil {
		return fmt.Errorf("could not add device under control, error: %s", err)
	}
	remoteConnectURL, err := getRemoteConnectURL(configs, serial)
	if err != nil {
		return fmt.Errorf("could not get remote connect URL, error: %s", err)
	}
	if err := connectToAdb(remoteConnectURL); err != nil {
		return fmt.Errorf("could not connect to ADB, error: %s", err)
	}
	return nil
}

func createConfigsModelFromEnvs() configsModel {
	return configsModel{
		stfHostURL:        os.Getenv("stf_host_url"),
		stfAccessToken:    os.Getenv("stf_access_token"),
		deviceFilter:      getEnvOrDefault("device_filter", "."),
		deviceNumberLimit: parseIntSafely(getEnvOrDefault("device_number_limit", "0")),
		adbKeyPub:         os.Getenv("adb_key_pub"),
		adbKey:            os.Getenv("adb_key"),
	}
}

func getEnvOrDefault(key string, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

func parseIntSafely(limit string) int {
	i, err := strconv.Atoi(limit)
	if err != nil {
		return 0
	}
	return i
}

func (configs configsModel) dump() {
	log.Infof("Config:")
	log.Infof("STF host: %s", configs.stfHostURL)
	log.Infof("Device filter: %s", configs.deviceFilter)
	log.Infof("Device number limit: %d", configs.deviceNumberLimit)
}

func (configs *configsModel) validate() error {
	if !strings.HasPrefix(configs.stfHostURL, "http") {
		return fmt.Errorf("invalid STF host: %s", configs.stfHostURL)
	}
	if configs.stfAccessToken == "" {
		return errors.New("STF access token cannot be empty")
	}
	return nil
}

func (configs *configsModel) isAnyAdbKeySet() bool {
	return configs.adbKey != "" || configs.adbKeyPub != ""
}

func setAdbKeys(configs configsModel, homeDir string) error {
	if err := saveNonEmptyAdbKey(configs.adbKey, homeDir, "adbkey", 0600); err != nil {
		return err
	}
	if err := saveNonEmptyAdbKey(configs.adbKeyPub, homeDir, "adbkey.pub", 0644); err != nil {
		return err
	}
	if configs.isAnyAdbKeySet() {
		return exec.Command(getAdbPath(), "kill-server").Run()
	}
	return nil
}

func saveNonEmptyAdbKey(key, homeDir, fileName string, mode os.FileMode) error {
	if key != "" {
		adbKeyPath := filepath.Join(homeDir, ".android", fileName)
		if err := ioutil.WriteFile(adbKeyPath, []byte(key), mode); err != nil {
			return err
		}
	}
	return nil
}

func getHomeDir() (string, error) {
	currentUser, err := user.Current()
	if err != nil {
		return "", err
	}
	return currentUser.HomeDir, nil
}

func connectToAdb(remoteConnectURL string) error {
	log.Infof("Connecting ADB to %s", remoteConnectURL)
	command := exec.Command(getAdbPath(), "connect", remoteConnectURL)
	output, err := command.CombinedOutput()
	if err != nil {
		return err
	}
	log.Debugf(string(output))
	return nil
}

func getRemoteConnectURL(configs configsModel, serial string) (string, error) {
	req, err := http.NewRequest("POST", configs.stfHostURL+userDevicesEndpoint+"/"+serial+"/remoteConnect", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+configs.stfAccessToken)
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			log.Warnf("Failed to close response body, error: %s", err)
		}
	}()
	bodyBytes, err := ioutil.ReadAll(response.Body)
	if response.StatusCode != 200 {
		return "", fmt.Errorf("request failed, status: %s | body: %s", response.Status, string(bodyBytes))
	}
	if err != nil {
		return "", err
	}
	var remoteConnection RemoteConnection
	err = json.Unmarshal(bodyBytes, &remoteConnection)
	return remoteConnection.RemoteConnectURL, err
}

func addDeviceUnderControl(configs configsModel, serial string) error {
	device := &Device{Serial: serial}
	body, err := json.Marshal(device)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", configs.stfHostURL+userDevicesEndpoint, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+configs.stfAccessToken)
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	if err := response.Body.Close(); err != nil {
		return err
	}
	if response.StatusCode != 200 {
		return fmt.Errorf("request failed, status: %s", response.Status)
	}
	return nil
}

func getSerials(configs configsModel) ([]string, error) {
	req, err := http.NewRequest("GET", configs.stfHostURL+devicesEndpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+configs.stfAccessToken)

	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	defer func() {
		if err := response.Body.Close(); err != nil {
			log.Warnf("Failed to close response body, error: %s", err)
		}
	}()

	if response.StatusCode != 200 {
		return nil, fmt.Errorf("request failed, status: %s", response.Status)
	}

	cmd := exec.Command("jq", "-r", ".devices[] | select(.present and .owner == null and ("+configs.deviceFilter+")) | .serial")
	cmd.Stdin = response.Body

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("could not create GET devices list request, error: %s | output: %s", err, stderr.String())
	}

	serials := strings.Fields(stdout.String())
	if len(serials) == 0 {
		return nil, fmt.Errorf("could not find present, not used devices satisfying filter: %s", configs.deviceFilter)
	}
	shuffleSlice(serials)
	return serials, nil
}

func shuffleSlice(slice []string) {
	for i := range slice {
		j := random.Intn(i + 1)
		slice[i], slice[j] = slice[j], slice[i]
	}
}

func getAdbPath() string {
	androidHome := os.Getenv("ANDROID_HOME")
	if androidHome == "" {
		return "adb"
	}
	return filepath.Join(androidHome, "platform-tools", "adb")
}

func exportArrayWithEnvman(keyStr string, values []string) error {
	body, err := json.Marshal(values)
	if err != nil {
		return err
	}
	return exec.Command("bitrise", "envman", "add", "--key", keyStr, "--value", string(body)).Run()
}
