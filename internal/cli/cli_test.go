package cli

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/adrg/xdg"
	"github.com/stretchr/testify/assert"

	"github.com/snyk/snyk-ls/config"
	"github.com/snyk/snyk-ls/internal/cli/install"
	"github.com/snyk/snyk-ls/internal/testutil"
)

func Test_ExpandParametersFromConfig(t *testing.T) {
	testutil.UnitTest(t)
	config.CurrentConfig().SetOrganization("test-org")
	settings := config.CliSettings{
		Insecure:             true,
		Endpoint:             "test-endpoint",
		AdditionalParameters: []string{"--all-projects", "-d"},
	}
	config.CurrentConfig().SetCliSettings(settings)
	var cmd = []string{"a", "b"}
	cmd = SnykCli{}.ExpandParametersFromConfig(cmd)
	assert.Contains(t, cmd, "--insecure")
	assert.Contains(t, cmd, "--all-projects")
	assert.Contains(t, cmd, "-d")
}

func Test_ExpandParametersFromConfigNoAllProjectsForIac(t *testing.T) {
	testutil.UnitTest(t)
	config.CurrentConfig().SetOrganization("test-org")
	settings := config.CliSettings{
		Insecure:             true,
		Endpoint:             "test-endpoint",
		AdditionalParameters: []string{"--all-projects", "-d"},
	}
	config.CurrentConfig().SetCliSettings(settings)
	var cmd = []string{"a", "iac"}
	cmd = SnykCli{}.ExpandParametersFromConfig(cmd)
	assert.Contains(t, cmd, "--insecure")
	assert.NotContains(t, cmd, "--all-projects")
	assert.Contains(t, cmd, "-d")
}

//goland:noinspection GoErrorStringFormat
func Test_HandleErrors_MissingTokenError(t *testing.T) {
	t.Skip("This test cannot be run automatically, as long as auth is calling an external website.")
	// todo check if an endpoint that is an http mock can be used for auth
	t.Setenv(config.SnykTokenKey, "dummy")
	os.Unsetenv(config.SnykTokenKey)
	testutil.IntegTest(t)
	config.CurrentConfig().SetToken("")
	ctx := context.Background()
	path, err := install.NewInstaller().Find()
	if err != nil {
		t.Fatal(t, err)
	}
	config.CurrentConfig().SetCliPath(path)
	cli := SnykCli{}
	err = errors.New("exit status 2")

	retry := cli.HandleErrors(ctx, "`snyk` requires an authenticated account. Please run `snyk auth` and try again.", err)

	assert.True(t, retry)
	assert.Eventually(t, func() bool {
		return config.CurrentConfig().Authenticated()
	}, 5*time.Minute, 10*time.Millisecond, "Didn't install CLI after error, timed out after 5 minutes.")
}

func Test_Execute_HandlesErrors(t *testing.T) {
	// exit status 2: MissingApiTokenError: `snyk` requires an authenticated account. Please run `snyk auth` and try again.
	//    at Object.apiTokenExists (C:\snapshot\snyk\dist\cli\webpack:\snyk\src\lib\api-token.ts:22:11)
	t.Setenv(config.SnykTokenKey, "dummy")
	os.Unsetenv(config.SnykTokenKey)
	t.Skipf("opens authentication browser window, only activate for dev testing")
	testutil.IntegTest(t)
	testutil.NotOnWindows(t, "moving around CLI config, and file moves under Windows are not very resilient")
	config.CurrentConfig().SetToken("")
	path, err := install.NewInstaller().Find()
	if err != nil {
		t.Fatal(t, err)
	}
	// remove config for cli, to ensure no token
	cliConfig := xdg.Home + "/.config/configstore/snyk.json"
	cliConfigBackup := cliConfig + time.Now().String() + ".bak"
	_ = os.Rename(cliConfig, cliConfigBackup)
	defer func(oldpath, newpath string) {
		_ = os.Rename(oldpath, newpath)
	}(cliConfigBackup, cliConfig)

	config.CurrentConfig().SetCliPath(path)
	cli := SnykCli{}

	response, err := cli.Execute([]string{path, "test"}, ".")

	assert.Error(t, err, string(response))
	assert.Equal(t, "exit status 3", err.Error()) // no supported target files found
}

func TestAddConfigToEnv(t *testing.T) {
	testutil.UnitTest(t)
	cli := SnykCli{}
	config.CurrentConfig().SetOrganization("testOrg")
	config.CurrentConfig().SetCliSettings(config.CliSettings{Endpoint: "testEndpoint"})

	updatedEnv := cli.addConfigValuesToEnv([]string{})

	assert.Contains(t, updatedEnv, "SNYK_CFG_ORG="+config.CurrentConfig().GetOrganization())
	assert.Contains(t, updatedEnv, "SNYK_API="+config.CurrentConfig().CliSettings().Endpoint)
	assert.Contains(t, updatedEnv, "SNYK_TOKEN="+config.CurrentConfig().Token())
}

func TestGetCommand_AddsToEnvironmentAndSetsDir(t *testing.T) {
	testutil.UnitTest(t)
	config.CurrentConfig().SetOrganization("TestGetCommand_AddsToEnvironmentAndSetsDirOrg")

	cmd := SnykCli{}.getCommand([]string{"executable", "arg"}, os.TempDir())

	assert.Equal(t, os.TempDir(), cmd.Dir)
	assert.Contains(t, cmd.Env, "SNYK_CFG_ORG=TestGetCommand_AddsToEnvironmentAndSetsDirOrg")
}
