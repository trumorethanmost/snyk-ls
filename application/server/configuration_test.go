/*
 * © 2022 Snyk Limited All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"golang.org/x/oauth2"

	"github.com/snyk/go-application-framework/pkg/auth"
	"github.com/snyk/go-application-framework/pkg/configuration"

	"github.com/snyk/snyk-ls/application/config"
	"github.com/snyk/snyk-ls/application/di"
	"github.com/snyk/snyk-ls/domain/observability/ux"

	"github.com/snyk/snyk-ls/domain/snyk"
	"github.com/snyk/snyk-ls/internal/lsp"
	"github.com/snyk/snyk-ls/internal/testutil"
)

var sampleSettings = lsp.Settings{
	ActivateSnykOpenSource:     "false",
	ActivateSnykCode:           "false",
	ActivateSnykIac:            "false",
	Insecure:                   "true",
	Endpoint:                   "https://api.fake.snyk.io",
	AdditionalParams:           "--all-projects -d",
	AdditionalEnv:              "a=b;c=d",
	Path:                       "addPath",
	SendErrorReports:           "true",
	Token:                      "token",
	SnykCodeApi:                "https://deeproxy.fake.snyk.io",
	EnableSnykLearnCodeActions: "true",
}

func keyFoundInEnv(key string) bool {
	found := false
	env := os.Environ()
	fmt.Println(env)
	for _, v := range env {
		if strings.HasPrefix(v, key+"=") {
			found = true
			break
		}
	}
	return found
}

func Test_configureOauth_registersStorageCallback(t *testing.T) {
	testutil.UnitTest(t)
	di.TestInit(t)
	c := config.CurrentConfig()
	c.SetAuthenticationMethod(lsp.OAuthAuthentication)

	// a token that's set into the configuration
	token := oauth2.Token{
		AccessToken:  t.Name(),
		RefreshToken: t.Name(),
		Expiry:       time.Now().Add(1 * time.Hour),
	}

	configureOAuth(c, auth.RefreshToken)

	tokenReceived := make(chan bool, 1)
	di.Notifier().CreateListener(func(params any) {
		msg, ok := params.(lsp.AuthenticationParams)
		assert.True(t, ok, "Received unexpected message type %v", params)
		assert.Contains(t, msg.Token, token.AccessToken)
		tokenReceived <- true
	})
	marshal, err := json.Marshal(token)
	assert.NoError(t, err)
	err = c.Storage().Set(auth.CONFIG_KEY_OAUTH_TOKEN, string(marshal))
	assert.NoError(t, err)
	assert.Eventuallyf(t, func() bool {
		return <-tokenReceived
	}, 5*time.Second, 100*time.Millisecond, "token should have been received")
}

func Test_configureOauth_oauthProvider_created_with_injected_refreshMethod(t *testing.T) {
	testutil.UnitTest(t)
	di.TestInit(t)
	c := config.CurrentConfig()
	c.SetAuthenticationMethod(lsp.OAuthAuthentication)

	// an expired token that's set into the configuration
	token := oauth2.Token{
		AccessToken:  t.Name(),
		RefreshToken: t.Name(),
		Expiry:       time.Now().Add(-1 * time.Hour),
	}

	tokenBytes, err := json.Marshal(token)
	assert.NoError(t, err)

	c.SetToken(string(tokenBytes))

	// refresh func is replaced with func that sends true into a channel when called
	triggeredChan := make(chan bool, 1)
	testFunc := func(ctx context.Context, oauthConfig *oauth2.Config, token *oauth2.Token) (*oauth2.Token, error) {
		triggeredChan <- true
		token.Expiry = time.Now().Add(1 * time.Hour)
		return token, nil
	}

	// test interface to be able to access the Authenticator (oauthProvider implements it)
	type providerWithAccessibleAuthenticator interface {
		snyk.AuthenticationProvider
		Authenticator() auth.Authenticator
	}

	configureOAuth(c, testFunc)

	provider, ok := di.AuthenticationService().Provider().(providerWithAccessibleAuthenticator)
	assert.True(t, ok, "provider should be of type providerWithAccessibleAuthenticator")

	// AddAuthenticationHeader will trigger the refresh method
	_ = provider.Authenticator().AddAuthenticationHeader(httptest.NewRequest("GET", "/", nil))

	assert.Eventuallyf(t, func() bool {
		return <-triggeredChan
	}, 5*time.Second, 100*time.Millisecond, "refresh should have been triggered")
}

func Test_WorkspaceDidChangeConfiguration_Push(t *testing.T) {
	testutil.UnitTest(t)
	di.TestInit(t)
	loc := setupServer(t)

	t.Setenv("a", "")
	t.Setenv("c", "")
	params := lsp.DidChangeConfigurationParams{Settings: sampleSettings}
	_, err := loc.Client.Call(ctx, "workspace/didChangeConfiguration", params)
	if err != nil {
		t.Fatal(err, "error calling server")
	}

	c := config.CurrentConfig()
	conf := config.CurrentConfig().Engine().GetConfiguration()
	assert.Equal(t, false, c.IsSnykCodeEnabled())
	assert.Equal(t, false, c.IsSnykOssEnabled())
	assert.Equal(t, false, c.IsSnykIacEnabled())
	assert.True(t, c.CliSettings().Insecure)
	assert.True(t, conf.GetBool(configuration.INSECURE_HTTPS))
	assert.Equal(t, []string{"--all-projects", "-d"}, c.CliSettings().AdditionalOssParameters)
	assert.Equal(t, params.Settings.Endpoint, c.SnykApi())
	assert.Equal(t, params.Settings.Endpoint, conf.GetString(configuration.API_URL))
	assert.Equal(t, "b", os.Getenv("a"))
	assert.Equal(t, "d", os.Getenv("c"))
	assert.True(t, strings.Contains(os.Getenv("PATH"), "addPath"))
	assert.True(t, config.CurrentConfig().IsErrorReportingEnabled())
	assert.Equal(t, "token", config.CurrentConfig().Token())
	assert.Equal(t, sampleSettings.SnykCodeApi, config.CurrentConfig().SnykCodeApi())
	assert.Equal(t, sampleSettings.EnableSnykLearnCodeActions, strconv.FormatBool(c.IsSnykLearnCodeActionsEnabled()))
}

func callBackMock(_ context.Context, request *jrpc2.Request) (any, error) {
	jsonRPCRecorder.Record(*request)
	if request.Method() == "workspace/configuration" {
		return []lsp.Settings{sampleSettings}, nil
	}
	return nil, nil
}

func Test_WorkspaceDidChangeConfiguration_Pull(t *testing.T) {
	testutil.UnitTest(t)
	loc := setupCustomServer(t, callBackMock)

	_, err := loc.Client.Call(ctx, "initialize", lsp.InitializeParams{
		Capabilities: lsp.ClientCapabilities{
			Workspace: lsp.WorkspaceClientCapabilities{
				Configuration: true,
			},
		},
	})
	if err != nil {
		t.Fatal(err, "error calling server")
	}

	params := lsp.DidChangeConfigurationParams{Settings: lsp.Settings{}}
	ctx := context.Background()
	_, err = loc.Client.Call(ctx, "workspace/didChangeConfiguration", params)
	if err != nil {
		t.Fatal(err, "error calling server")
	}
	assert.NoError(t, err)

	c := config.CurrentConfig()
	conf := config.CurrentConfig().Engine().GetConfiguration()
	assert.Equal(t, false, c.IsSnykCodeEnabled())
	assert.Equal(t, false, c.IsSnykOssEnabled())
	assert.Equal(t, false, c.IsSnykIacEnabled())
	assert.True(t, c.CliSettings().Insecure)
	assert.True(t, conf.GetBool(configuration.INSECURE_HTTPS))
	assert.Equal(t, []string{"--all-projects", "-d"}, c.CliSettings().AdditionalOssParameters)
	assert.Equal(t, sampleSettings.Endpoint, c.SnykApi())
	assert.Equal(t, c.SnykApi(), conf.GetString(configuration.API_URL))
	assert.True(t, config.CurrentConfig().IsErrorReportingEnabled())
	assert.Equal(t, "token", config.CurrentConfig().Token())
	assert.Equal(t, sampleSettings.SnykCodeApi, config.CurrentConfig().SnykCodeApi())
	assert.Equal(t, sampleSettings.EnableSnykLearnCodeActions, strconv.FormatBool(c.IsSnykLearnCodeActionsEnabled()))
}

func Test_WorkspaceDidChangeConfiguration_PullNoCapability(t *testing.T) {
	testutil.UnitTest(t)
	loc := setupCustomServer(t, callBackMock)

	params := lsp.DidChangeConfigurationParams{Settings: lsp.Settings{}}
	ctx := context.Background()
	var updated = true
	err := loc.Client.CallResult(ctx, "workspace/didChangeConfiguration", params, &updated)
	if err != nil {
		t.Fatal(err, "error calling server")
	}

	assert.NoError(t, err)
	assert.False(t, updated)
}

func Test_UpdateSettings(t *testing.T) {
	testutil.UnitTest(t)
	di.TestInit(t)

	orgUuid, _ := uuid.NewRandom()
	expectedOrgId := orgUuid.String()

	t.Run("snykgov.io substring endpoint enables oauth authentication in init", func(t *testing.T) {
		endpoint := "https://app.fedramp.snykgov.io/api/v1"
		updateApiEndpoints(lsp.Settings{Endpoint: endpoint}, true)
		assert.Equal(t, lsp.OAuthAuthentication, config.CurrentConfig().AuthenticationMethod())
	})

	t.Run("All settings are updated", func(t *testing.T) {
		config.SetCurrentConfig(config.New())

		settings := lsp.Settings{
			ActivateSnykOpenSource:      "false",
			ActivateSnykCode:            "false",
			ActivateSnykIac:             "false",
			Insecure:                    "true",
			Endpoint:                    "https://snyk.io/api",
			AdditionalParams:            "--all-projects -d",
			AdditionalEnv:               "a=b;c=d",
			Path:                        "addPath",
			SendErrorReports:            "true",
			Organization:                expectedOrgId,
			EnableTelemetry:             "false",
			ManageBinariesAutomatically: "false",
			CliPath:                     "C:\\Users\\CliPath\\snyk-ls.exe",
			Token:                       "a fancy token",
			FilterSeverity:              lsp.DefaultSeverityFilter(),
			TrustedFolders:              []string{"trustedPath1", "trustedPath2"},
			OsPlatform:                  "windows",
			OsArch:                      "amd64",
			RuntimeName:                 "java",
			RuntimeVersion:              "1.8.0_275",
			ScanningMode:                "manual",
			AuthenticationMethod:        lsp.OAuthAuthentication,
			SnykCodeApi:                 sampleSettings.SnykCodeApi,
		}

		UpdateSettings(settings)

		c := config.CurrentConfig()
		assert.Equal(t, false, c.IsSnykCodeEnabled())
		assert.Equal(t, false, c.IsSnykOssEnabled())
		assert.Equal(t, false, c.IsSnykIacEnabled())
		assert.Equal(t, true, c.CliSettings().Insecure)
		assert.Equal(t, []string{"--all-projects", "-d"}, c.CliSettings().AdditionalOssParameters)
		assert.Equal(t, "https://snyk.io/api", c.SnykApi())
		assert.Equal(t, "b", os.Getenv("a"))
		assert.Equal(t, "d", os.Getenv("c"))
		assert.True(t, strings.HasPrefix(os.Getenv("PATH"), "addPath"+string(os.PathListSeparator)))
		assert.True(t, c.IsErrorReportingEnabled())
		assert.Equal(t, expectedOrgId, c.Organization())
		assert.False(t, c.IsTelemetryEnabled())
		assert.False(t, c.ManageBinariesAutomatically())
		assert.Equal(t, "C:\\Users\\CliPath\\snyk-ls.exe", c.CliSettings().Path())
		assert.Equal(t, "a fancy token", c.Token())
		assert.Equal(t, lsp.DefaultSeverityFilter(), c.FilterSeverity())
		assert.Subset(t, []string{"trustedPath1", "trustedPath2"}, c.TrustedFolders())
		assert.Equal(t, settings.OsPlatform, c.OsPlatform())
		assert.Equal(t, settings.OsArch, c.OsArch())
		assert.Equal(t, settings.RuntimeName, c.RuntimeName())
		assert.Equal(t, settings.RuntimeVersion, c.RuntimeVersion())
		assert.False(t, c.IsAutoScanEnabled())
		assert.Equal(t, lsp.OAuthAuthentication, c.AuthenticationMethod())
		assert.Equal(t, sampleSettings.SnykCodeApi, c.SnykCodeApi())
	})

	t.Run("empty snyk code api is ignored and default is used", func(t *testing.T) {
		config.SetCurrentConfig(config.New())

		UpdateSettings(lsp.Settings{})

		c := config.CurrentConfig()
		assert.Equal(t, config.DefaultDeeproxyApiUrl, c.SnykCodeApi())
	})

	t.Run("blank organisation is ignored", func(t *testing.T) {
		c := config.New()
		config.SetCurrentConfig(c)
		c.SetOrganization(expectedOrgId)

		UpdateSettings(lsp.Settings{Organization: " "})

		assert.Equal(t, expectedOrgId, c.Organization())
	})

	t.Run("incomplete env vars", func(t *testing.T) {
		config.SetCurrentConfig(config.New())

		UpdateSettings(lsp.Settings{AdditionalEnv: "a="})

		assert.Empty(t, os.Getenv("a"))
	})

	t.Run("empty env vars", func(t *testing.T) {
		config.SetCurrentConfig(config.New())
		varCount := len(os.Environ())

		UpdateSettings(lsp.Settings{AdditionalEnv: " "})

		assert.Equal(t, varCount, len(os.Environ()))
	})

	t.Run("broken env variables", func(t *testing.T) {
		config.SetCurrentConfig(config.New())

		UpdateSettings(lsp.Settings{AdditionalEnv: "a=; b"})

		assert.Empty(t, os.Getenv("a"))
		assert.Empty(t, os.Getenv("b"))
		assert.Empty(t, os.Getenv(";"))
	})
	t.Run("trusted folders", func(t *testing.T) {
		config.SetCurrentConfig(config.New())

		UpdateSettings(lsp.Settings{TrustedFolders: []string{"/a/b", "/b/c"}})

		c := config.CurrentConfig()
		assert.Contains(t, c.TrustedFolders(), "/a/b")
		assert.Contains(t, c.TrustedFolders(), "/b/c")
	})

	t.Run("manage binaries automatically", func(t *testing.T) {
		t.Run("true", func(t *testing.T) {
			UpdateSettings(lsp.Settings{
				ManageBinariesAutomatically: "true",
			})

			assert.True(t, config.CurrentConfig().ManageBinariesAutomatically())
		})
		t.Run("false", func(t *testing.T) {
			UpdateSettings(lsp.Settings{
				ManageBinariesAutomatically: "false",
			})

			assert.False(t, config.CurrentConfig().ManageBinariesAutomatically())
		})

		t.Run("invalid value does not update", func(t *testing.T) {
			UpdateSettings(lsp.Settings{
				ManageBinariesAutomatically: "true",
			})

			UpdateSettings(lsp.Settings{
				ManageBinariesAutomatically: "dog",
			})

			assert.True(t, config.CurrentConfig().ManageBinariesAutomatically())
		})
	})

	t.Run("activateSnykCodeSecurity is passed", func(t *testing.T) {
		config.SetCurrentConfig(config.New())

		UpdateSettings(lsp.Settings{ActivateSnykCodeSecurity: "true"})

		c := config.CurrentConfig()
		assert.Equal(t, true, c.IsSnykCodeSecurityEnabled())
	})
	t.Run("activateSnykCodeSecurity is not passed", func(t *testing.T) {
		config.SetCurrentConfig(config.New())

		UpdateSettings(lsp.Settings{})

		c := config.CurrentConfig()
		assert.Equal(t, false, c.IsSnykCodeSecurityEnabled())

		config.SetCurrentConfig(config.New())
		c = config.CurrentConfig()
		c.EnableSnykCodeSecurity(true)

		UpdateSettings(lsp.Settings{})

		assert.Equal(t, true, c.IsSnykCodeSecurityEnabled())
	})
	t.Run("activateSnykCodeQuality is passed", func(t *testing.T) {
		config.SetCurrentConfig(config.New())

		UpdateSettings(lsp.Settings{ActivateSnykCodeQuality: "true"})

		c := config.CurrentConfig()
		assert.Equal(t, true, c.IsSnykCodeQualityEnabled())
	})
	t.Run("activateSnykCodeQuality is not passed", func(t *testing.T) {
		config.SetCurrentConfig(config.New())

		UpdateSettings(lsp.Settings{})

		c := config.CurrentConfig()
		assert.Equal(t, false, c.IsSnykCodeQualityEnabled())

		config.SetCurrentConfig(config.New())
		c = config.CurrentConfig()
		c.EnableSnykCodeQuality(true)

		UpdateSettings(lsp.Settings{})

		assert.Equal(t, true, c.IsSnykCodeQualityEnabled())
	})
	t.Run("activateSnykCode sets SnykCodeQuality and SnykCodeSecurity", func(t *testing.T) {
		config.SetCurrentConfig(config.New())

		UpdateSettings(lsp.Settings{
			ActivateSnykCode: "true",
		})

		c := config.CurrentConfig()
		assert.Equal(t, true, c.IsSnykCodeQualityEnabled())
		assert.Equal(t, true, c.IsSnykCodeSecurityEnabled())
		assert.Equal(t, true, c.IsSnykCodeEnabled())
	})

	t.Run("severity filter", func(t *testing.T) {
		config.SetCurrentConfig(config.New())
		t.Run("filtering gets passed", func(t *testing.T) {
			mixedSeverityFilter := lsp.NewSeverityFilter(true, false, true, false)
			UpdateSettings(lsp.Settings{FilterSeverity: mixedSeverityFilter})

			c := config.CurrentConfig()
			assert.Equal(t, mixedSeverityFilter, c.FilterSeverity())
		})
	})
}

func Test_ScanningModeChanged_AnalyticsNotified(t *testing.T) {
	testutil.UnitTest(t)
	di.TestInit(t)
	config.SetCurrentConfig(config.New())
	analytics := di.Analytics().(*ux.TestAnalytics)
	callCount := analytics.ScanModeIsSelectedCount

	UpdateSettings(lsp.Settings{ScanningMode: "manual"})

	assert.Equal(t, callCount+1, analytics.ScanModeIsSelectedCount)
}

func Test_InitializeSettings(t *testing.T) {
	testutil.UnitTest(t)
	di.TestInit(t)

	t.Run("device ID is passed", func(t *testing.T) {
		config.SetCurrentConfig(config.New())
		deviceId := "test-device-id"

		InitializeSettings(lsp.Settings{DeviceId: deviceId})

		c := config.CurrentConfig()
		assert.Equal(t, deviceId, c.DeviceID())
	})

	t.Run("device ID is not passed", func(t *testing.T) {
		config.SetCurrentConfig(config.New())
		curentDeviceId := config.CurrentConfig().DeviceID()

		InitializeSettings(lsp.Settings{})

		c := config.CurrentConfig()
		assert.Equal(t, curentDeviceId, c.DeviceID())
	})

	t.Run("activateSnykCodeSecurity is passed", func(t *testing.T) {
		config.SetCurrentConfig(config.New())

		InitializeSettings(lsp.Settings{ActivateSnykCodeSecurity: "true"})

		c := config.CurrentConfig()
		assert.Equal(t, true, c.IsSnykCodeSecurityEnabled())
	})
	t.Run("activateSnykCodeSecurity is not passed", func(t *testing.T) {
		config.SetCurrentConfig(config.New())

		InitializeSettings(lsp.Settings{})

		c := config.CurrentConfig()
		assert.Equal(t, false, c.IsSnykCodeSecurityEnabled())

		config.SetCurrentConfig(config.New())
		c = config.CurrentConfig()
		c.EnableSnykCodeSecurity(true)

		InitializeSettings(lsp.Settings{})

		assert.Equal(t, true, c.IsSnykCodeSecurityEnabled())
	})
	t.Run("activateSnykCodeQuality is passed", func(t *testing.T) {
		config.SetCurrentConfig(config.New())

		InitializeSettings(lsp.Settings{ActivateSnykCodeQuality: "true"})

		c := config.CurrentConfig()
		assert.Equal(t, true, c.IsSnykCodeQualityEnabled())
	})
	t.Run("activateSnykCodeQuality is not passed", func(t *testing.T) {
		config.SetCurrentConfig(config.New())

		InitializeSettings(lsp.Settings{})

		c := config.CurrentConfig()
		assert.Equal(t, false, c.IsSnykCodeQualityEnabled())

		config.SetCurrentConfig(config.New())
		c = config.CurrentConfig()
		c.EnableSnykCodeQuality(true)

		InitializeSettings(lsp.Settings{})

		assert.Equal(t, true, c.IsSnykCodeQualityEnabled())
	})

	t.Run("authenticationMethod is passed", func(t *testing.T) {
		testutil.UnitTest(t)
		di.TestInit(t)
		c := config.CurrentConfig()
		assert.Equal(t, lsp.TokenAuthentication, c.AuthenticationMethod())

		InitializeSettings(lsp.Settings{AuthenticationMethod: lsp.OAuthAuthentication})

		assert.Equal(t, lsp.OAuthAuthentication, c.AuthenticationMethod())
	})

	t.Run("custom path configuration", func(t *testing.T) {
		testutil.UnitTest(t)

		first := "first"
		second := "second"

		upperCasePathKey := "PATH"
		caseSensitivePathKey := "Path"
		t.Setenv(caseSensitivePathKey, "something_meaningful")

		// update path to hold a custom value
		UpdateSettings(lsp.Settings{Path: first})
		assert.True(t, strings.HasPrefix(os.Getenv(upperCasePathKey), first+string(os.PathListSeparator)))

		// update path to hold another value
		UpdateSettings(lsp.Settings{Path: second})
		assert.True(t, strings.HasPrefix(os.Getenv(upperCasePathKey), second+string(os.PathListSeparator)))
		assert.False(t, strings.Contains(os.Getenv(upperCasePathKey), first))

		// reset path with non-empty settings
		UpdateSettings(lsp.Settings{Path: "", AuthenticationMethod: "token"})
		assert.False(t, strings.Contains(os.Getenv(upperCasePathKey), second))

		assert.True(t, keyFoundInEnv(upperCasePathKey))
		assert.False(t, keyFoundInEnv(caseSensitivePathKey))
	})

}
