package main

import (
	"net/http"
	"time"
	"os/exec"
	"bytes"
	"log"
	"strings"
	"os"
	"encoding/json"
	"io/ioutil"
	"path/filepath"
	"os/user"
	"math/rand"
)

type Device struct {
	Serial string `json:"serial"`
}

type RemoteConnection struct {
	RemoteConnectUrl string `json:"remoteConnectUrl"`
}

const devicesEndpoint = "/api/v1/devices"
const userDevicesEndpoint = "/api/v1/user/devices"

const stfHostUrl = os.Getenv("stf_host_url")
const stfToken = os.Getenv("stf_token")
const deviceQuery = os.Getenv("device_query")
const deviceNumberLimit = os.Getenv("device_number_limit")
const adbKeyPub = os.Getenv("adb_key_pub")
const adbKey = os.Getenv("adb_key")

var client = &http.Client{Timeout: time.Second * 10}

func main() {
	serials := getSerials()
	setAdbKeys()
	exportArrayWithEnvman("STF_DEVICE_SERIALS", serials)

	for _, serial := range serials {
		addDevice(serial)
		remoteConnectUrl := getRemoteConnectUrl(serial)
		connectToAdb(remoteConnectUrl)
	}
}

func setAdbKeys() {
	usr, err := user.Current()
	if err != nil {
		log.Fatalf("Could not determine current user %s", err)
	}

	adbKeyPath := filepath.Join(usr.HomeDir, ".android", "adbkey")
	writeFile(adbKeyPath, adbKey, 0644)

	adbKeyPubPath := filepath.Join(usr.HomeDir, ".android", "adbkey.pub")
	writeFile(adbKeyPubPath, adbKeyPub, 0600)

	err = exec.Command(getAdbPath(), "kill-server").Run()
	if err != nil {
		log.Fatalf("Could not restart ADB server %s", err)
	}
}

func writeFile(path string, content string, perm os.FileMode) {
	data := []byte(content)
	err := ioutil.WriteFile(path, data, perm)
	if err != nil {
		log.Fatalf("Could not write file %s %s", path, err)
	}
}

func connectToAdb(remoteConnectUrl string) {
	log.Printf("Connecting ADB to %s\n", remoteConnectUrl)
	command := exec.Command(getAdbPath(), "connect", remoteConnectUrl)
	output, err := command.CombinedOutput()
	if err != nil {
		log.Fatalf("ADB could not connect to %s %s", remoteConnectUrl, err)
	}
	log.Println(string(output))
}

func getRemoteConnectUrl(serial string) string {
	req, _ := http.NewRequest("POST", stfHostUrl + userDevicesEndpoint + "/" + serial + "/remoteConnect", nil)
	req.Header.Set("Authorization", "Bearer " + stfToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("Could not get remote connect URL for: %s %s", serial, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Fatalf("Could not get remote connect URL for: %s %s", serial, resp.Status)
	}
	bodyBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("Could not get remote connect URL for: %s %s", serial, err)
	}
	var remoteConnection RemoteConnection
	err = json.Unmarshal(bodyBytes, &remoteConnection)
	if err != nil {
		log.Fatalf("Could not get remote connect URL for: %s %s", serial, err)
	}
	return remoteConnection.RemoteConnectUrl
}

func addDevice(serial string) {
	device := &Device{Serial: serial}
	body, _ := json.Marshal(device)
	req, _ := http.NewRequest("POST", stfHostUrl + userDevicesEndpoint, bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer " + stfToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("Could not add device with serial: %s under control: %s", serial, err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Fatalf("Could not add device with serial: %s under control: %s", serial, resp.Status)
	}
}

func getSerials() []string {
	req, _ := http.NewRequest("GET", stfHostUrl + devicesEndpoint, nil)
	req.Header.Set("Authorization", "Bearer " + stfToken)

	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("Could not get devices list: %s", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Fatalf("Could not get devices list: %s", resp.Status)
	}

	cmd := exec.Command("jq", "-r", ".devices[] | select(.present) | select(" + deviceQuery + ") | .serial")
	cmd.Stdin = resp.Body

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err != nil {
		log.Fatalf("Could not create GET devices list request: %s %s", err, stderr.String())
	}

	serials := strings.Fields(stdout.String())
	if len(serials) == 0 {
		log.Fatalf("Could not find present devices satisfying query: %s", deviceQuery)
	}
	shuffle(serials)
	if (deviceNumberLimit > len(serials)) {
		return serials[:deviceNumberLimit]
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

