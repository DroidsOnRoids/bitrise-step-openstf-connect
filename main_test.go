package main

import (
	"testing"
	"github.com/stretchr/testify/require"
	"os"
	"io/ioutil"
	"path/filepath"
	"os/exec"
	"time"
)

func TestGetEnvOrDefault(t *testing.T) {
	require.Equal(t, os.Getenv("HOME"), getEnvOrDefault("HOME", "test"))
	require.Equal(t, "test", getEnvOrDefault("NON_EXISTENT_ENV", "test"))
}

func TestParseIntSafely(t *testing.T) {
	require.Equal(t, 1, parseIntSafely("1"))
	require.Equal(t, 0, parseIntSafely(""))
	require.Equal(t, 0, parseIntSafely("test"))
}

func TestValidateConfigNoHostUrl(t *testing.T) {
	configs := configsModel{stfAccessToken:"test"}
	require.Error(t, configs.validate())
}

func TestValidateConfigNoAccessToken(t *testing.T) {
	configs := configsModel{stfHostURL:"http://test.test"}
	require.Error(t, configs.validate())
}

func TestValidateConfigNoErrors(t *testing.T) {
	configs := configsModel{stfHostURL:"http://test.test", stfAccessToken:"test"}
	require.NoError(t, configs.validate())
}

func TestIsAnyAdbKeySetNoKeys(t *testing.T) {
	configs := configsModel{}
	require.False(t, configs.isAnyAdbKeySet())
}

func TestIsAnyAdbKeySetPrivateOnly(t *testing.T) {
	configs := configsModel{adbKey:"test"}
	require.True(t, configs.isAnyAdbKeySet())
}

func TestIsAnyAdbKeySetPublicOnly(t *testing.T) {
	configs := configsModel{adbKeyPub:"test"}
	require.True(t, configs.isAnyAdbKeySet())
}

func TestSaveNonEmptyAdbKeyNoOp(t *testing.T) {
	fakeHomeDir, fakeAndroidUserDir := prepareFakeAndroidHomeDir(t)

	keyFileName := "file"
	require.NoError(t, saveNonEmptyAdbKey("", fakeHomeDir, keyFileName, 0644))

	info, err := os.Stat(filepath.Join(fakeAndroidUserDir, keyFileName))
	require.Error(t, err)
	require.Nil(t, info)

	require.NoError(t, os.RemoveAll(fakeHomeDir))
}

func TestSaveNonEmptyAdbKeySuccess(t *testing.T) {
	fakeHomeDir, fakeAndroidUserDir := prepareFakeAndroidHomeDir(t)
	keyFileName := "file"
	fakeKey := "key"
	var mode os.FileMode = 0644

	require.NoError(t, saveNonEmptyAdbKey(fakeKey, fakeHomeDir, keyFileName, mode))

	filePath := filepath.Join(fakeAndroidUserDir, keyFileName)
	requireFile(t, filePath, fakeKey, mode)
	require.NoError(t, os.RemoveAll(fakeHomeDir))
}

func TestSaveNonEmptyAdbKeyFail(t *testing.T) {
	fakeHomeDir, fakeAndroidUserDir := prepareFakeAndroidHomeDir(t)
	keyFileName := "file"
	fakeKey := "key"
	var mode os.FileMode = 0644

	require.NoError(t, os.Chmod(fakeAndroidUserDir, 000))
	require.Error(t, saveNonEmptyAdbKey(fakeKey, fakeHomeDir, keyFileName, mode))

	require.NoError(t, os.RemoveAll(fakeHomeDir))
}

func TestSetAdbKeys(t *testing.T) {
	configs := configsModel{adbKey:"private", adbKeyPub:"public"}
	fakeHomeDir, fakeAndroidUserDir := prepareFakeAndroidHomeDir(t)

	require.NoError(t, exec.Command("adb", "devices").Run())
	oldAdbPids, err := exec.Command("pgrep", "adb").CombinedOutput()
	require.NoError(t, err)

	require.NoError(t, setAdbKeys(configs, fakeHomeDir))

	privateKeyFile := filepath.Join(fakeAndroidUserDir, "adbkey")
	requireFile(t, privateKeyFile, configs.adbKey, 0600)

	publicKeyFile := filepath.Join(fakeAndroidUserDir, "adbkey.pub")
	requireFile(t, publicKeyFile, configs.adbKeyPub, 0644)

	err = retry(5, 50 * time.Millisecond, func() (err error) {
		err = exec.Command("adb", "devices").Run()
		return
	})
	require.NoError(t, err)

	newAdbPids, err := exec.Command("pgrep", "adb").CombinedOutput()
	require.NoError(t, err)
	require.NotEqual(t, string(newAdbPids), string(oldAdbPids))

	require.NoError(t, os.RemoveAll(fakeHomeDir))
}

func retry(attempts int, sleep time.Duration, callback func() error) (err error) {
	for i := 0; ; i++ {
		err = callback()
		if err == nil {
			return
		}
		if i >= (attempts - 1) {
			break
		}
		time.Sleep(sleep)
	}
	return err
}

func requireFile(t *testing.T, filePath, content string, mode os.FileMode) {
	bytes, err := ioutil.ReadFile(filePath)
	require.NoError(t, err)
	require.Equal(t, content, string(bytes))
	info, err := os.Stat(filePath)
	require.NoError(t, err)
	require.Equal(t, mode, info.Mode().Perm())
}

func prepareFakeAndroidHomeDir(t *testing.T) (string, string) {
	fakeHomeDir, err := ioutil.TempDir("", "stf_android_test")
	require.NoError(t, err)
	fakeAndroidUserDir := filepath.Join(fakeHomeDir, ".android")
	require.NoError(t, os.Mkdir(fakeAndroidUserDir, 0777))
	return fakeHomeDir, fakeAndroidUserDir
}

func TestGetHomeDir(t *testing.T) {
	homeDir, err := getHomeDir()
	require.NoError(t, err)
	require.Equal(t, os.Getenv("HOME"), homeDir)
}

func TestGetAdbPath(t *testing.T) {
	adbPath := getAdbPath()
	info, err := os.Stat(adbPath)
	require.NoError(t, err)
	require.False(t, info.IsDir())
}