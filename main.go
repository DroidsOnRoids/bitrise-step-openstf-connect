package main

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"fmt"
	"errors"
)

type ConfigsModel struct {
	stfHostUrl        string
	stfAccessToken    string
	deviceQuery       string
	deviceNumberLimit int
	adbKeyPub         string
	adbKey            string
}

type Device struct {
	Serial string `json:"serial"`
}

type RemoteConnection struct {
	RemoteConnectUrl string `json:"remoteConnectUrl"`
}

const devicesEndpoint = "/api/v1/devices"
const userDevicesEndpoint = "/api/v1/user/devices"

var client = &http.Client{Timeout: time.Second * 10}

func createConfigsModelFromEnvs() ConfigsModel {
	return ConfigsModel{
		stfHostUrl:        os.Getenv("stf_host_url"),
		stfAccessToken:    os.Getenv("stf_access_token"),
		deviceQuery:       os.Getenv("device_query"),
		deviceNumberLimit: getDeviceNumberLimitFromEnv(),
		adbKeyPub:         os.Getenv("adb_key_pub"),
		adbKey:            os.Getenv("adb_key"),
	}
}

func getDeviceNumberLimitFromEnv() int {
	envDeviceNumberLimit := os.Getenv("device_number_limit")
	if envDeviceNumberLimit == "" {
		return 0
	}
	i, err := strconv.Atoi(envDeviceNumberLimit)
	if err != nil {
		log.Fatalf("Device number limit: '%s' is not a number nor empty string", envDeviceNumberLimit)
	}
	return i
}

func (configs ConfigsModel) dump() {
	log.Println("Configs:")
	log.Printf("STF host           : %s", configs.stfHostUrl)
	log.Printf("Device query       : %s", configs.deviceQuery)
	log.Printf("Device number limit: %d", configs.deviceNumberLimit)
}

func (configs ConfigsModel) validate() error {
	if !strings.HasPrefix(configs.stfHostUrl, "http") {
		return fmt.Errorf("Invalid STF host: %s", configs.stfHostUrl)
	}
	if configs.stfAccessToken == "" {
		return errors.New("STF access token cannot be empty")
	}
	if configs.deviceQuery == "" {
		configs.deviceQuery = "."
	}
	return nil
}

func main() {
	configs := createConfigsModelFromEnvs()
	configs.dump()
	if err := configs.validate(); err != nil {
		log.Fatal(err)
	}

	serials := getSerials(configs)
	setAdbKeys(configs)
	exportArrayWithEnvman("STF_DEVICE_SERIAL_LIST", serials)

	for _, serial := range serials {
		addDevice(configs, serial)
		remoteConnectUrl := getRemoteConnectUrl(configs, serial)
		connectToAdb(remoteConnectUrl)
	}
}

func setAdbKeys(configs ConfigsModel) {
	usr, err := user.Current()
	if err != nil {
		log.Fatalf("Could not determine current user, error: %s", err)
	}

	adbServerRestartNeeded := false
	if configs.adbKey != "" {
		adbKeyPath := filepath.Join(usr.HomeDir, ".android", "adbkey")
		writeFile(adbKeyPath, configs.adbKey, 0644)
		adbServerRestartNeeded = true
	}

	if configs.adbKeyPub != "" {
		adbKeyPubPath := filepath.Join(usr.HomeDir, ".android", "adbkey.pub")
		writeFile(adbKeyPubPath, configs.adbKeyPub, 0600)
		adbServerRestartNeeded = true
	}

	if (adbServerRestartNeeded) {
		err = exec.Command(getAdbPath(), "kill-server").Run()
		if err != nil {
			log.Fatalf("Could not restart ADB server, error: %s", err)
		}
	}
}

func writeFile(path string, content string, perm os.FileMode) {
	data := []byte(content)
	err := ioutil.WriteFile(path, data, perm)
	if err != nil {
		log.Fatalf("Could not write file %s, error: %s", path, err)
	}
}

func connectToAdb(remoteConnectUrl string) {
	log.Printf("Connecting ADB to %s", remoteConnectUrl)
	command := exec.Command(getAdbPath(), "connect", remoteConnectUrl)
	output, err := command.CombinedOutput()
	if err != nil {
		log.Fatalf("ADB could not connect to %s, error: %s", remoteConnectUrl, err)
	}
	log.Println(string(output))
}

func getRemoteConnectUrl(configs ConfigsModel, serial string) string {
	req, _ := http.NewRequest("POST", configs.stfHostUrl + userDevicesEndpoint + "/" + serial + "/remoteConnect", nil)
	req.Header.Set("Authorization", "Bearer " + configs.stfAccessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("Could not get remote connect URL for: %s, error: %s", serial, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Fatalf("Could not get remote connect URL for: %s, error: %s", serial, resp.Status)
	}
	bodyBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("Could not get remote connect URL for: %s, error: %s", serial, err)
	}
	var remoteConnection RemoteConnection
	err = json.Unmarshal(bodyBytes, &remoteConnection)
	if err != nil {
		log.Fatalf("Could not get remote connect URL for: %s, error: %s", serial, err)
	}
	return remoteConnection.RemoteConnectUrl
}

func addDevice(configs ConfigsModel, serial string) {
	device := &Device{Serial: serial}
	body, _ := json.Marshal(device)
	req, _ := http.NewRequest("POST", configs.stfHostUrl + userDevicesEndpoint, bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer " + configs.stfAccessToken)
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		log.Fatalf("Could not add device with serial: %s under control, error: %s", serial, err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		log.Fatalf("Could not add device with serial: %s under control, error: %s", serial, response.Status)
	}
}

func getSerials(configs ConfigsModel) []string {
	req, _ := http.NewRequest("GET", configs.stfHostUrl + devicesEndpoint, nil)
	req.Header.Set("Authorization", "Bearer " + configs.stfAccessToken)

	response, err := client.Do(req)
	if err != nil {
		log.Fatalf("Could not get devices list, error: %s", err)
	}

	defer response.Body.Close()
	if response.StatusCode != 200 {
		log.Fatalf("Could not get devices list: %s, error:", response.Status)
	}

	cmd := exec.Command("jq", "-r", ".devices[] | select(.present and .using == false and (" + configs.deviceQuery + ")) | .serial")
	cmd.Stdin = response.Body

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err != nil {
		log.Fatalf("Could not create GET devices list request, error: %s | output: %s", err, stderr.String())
	}

	serials := strings.Fields(stdout.String())
	if len(serials) == 0 {
		log.Fatalf("Could not find present, not used devices satisfying query: %s", configs.deviceQuery)
	}
	shuffle(serials)
	if configs.deviceNumberLimit > 0 && configs.deviceNumberLimit < len(serials) {
		return serials[:configs.deviceNumberLimit]
	}
	return serials
}

func shuffle(slice []string) {
	for i := range slice {
		j := rand.Intn(i + 1)
		slice[i], slice[j] = slice[j], slice[i]
	}
}

func getAdbPath() string {
	android_home := os.Getenv("ANDROID_HOME")
	if android_home == "" {
		return "adb"
	}
	return filepath.Join(android_home, "platform-tools", "adb")
}

func exportArrayWithEnvman(keyStr string, values []string) {
	body, err := json.Marshal(values)
	if err != nil {
		log.Fatalf("Failed to expose output with envman, error: %s", err)
	}
	cmdLog, err := exec.Command("bitrise", "envman", "add", "--key", keyStr, "--value", string(body)).CombinedOutput()
	if err != nil {
		log.Fatalf("Failed to expose output with envman, error: %s | output: %s", err, cmdLog)
	}
}
