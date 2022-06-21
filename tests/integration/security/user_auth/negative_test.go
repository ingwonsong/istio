//go:build integ
// +build integ

// Copyright Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package userauth

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"

	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/tests/integration/security/user_auth/selenium"
	"istio.io/istio/tests/integration/security/user_auth/util"
)

const (
	userAuthNS        = "asm-user-auth"
	secretName        = "oauth-secret"
	clientID          = "clientID"
	clientSecret      = "clientSecret"
	dummyClientID     = "dGVzdGNsaWVudGlk"
	dummyClientSecret = "dGVzdGNsaWVudHNlY3JldA=="
	authservice       = "authservice"
	localhostURL      = "https://localhost:8443"
)

var secretClient corev1.SecretInterface

// TestMisconfiguration Test following misconfigurations for User Auth:
// 1. Client ID in OAuth secret
// 2. Client Secret in OAuth secret
// 3. Redirect URL in UserAuthConfig
// 4. Issuer URL in UserAuthConfig
func TestMisconfiguration(t *testing.T) {
	// nolint: staticcheck
	framework.
		NewTest(t).
		RequiresSingleCluster().
		Features("security.user.auth").
		Run(func(ctx framework.TestContext) {
			util.SetupConfig(ctx)
			// port-forward to cluster
			forwarder := util.GetIngressPortForwarderOrFail(ctx, ist, localPort, ingressPort)
			if err := forwarder.Start(); err != nil {
				t.Fatalf("failed starting port forwarder for ingress: %v", err)
			}
			// check the port-forward availability
			util.ValidatePortForward(ctx, strconv.Itoa(localPort))

			// Get clients
			cluster := ctx.Clusters().Default()
			secretClient = cluster.Kube().CoreV1().Secrets(userAuthNS)
			oauthSecret, err := secretClient.Get(context.TODO(), secretName, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("failed getting oauth secret: %v", err)
			}
			originalClientID := oauthSecret.Data[clientID]
			originalClientSecret := oauthSecret.Data[clientSecret]

			// Client secret misconfiguration should fail authentication
			ctx.NewSubTest("invalidClientSecret").Run(func(ctx framework.TestContext) {
				updateSecretOrFail(ctx, secretName, []string{clientID, clientSecret}, [][]byte{originalClientID, []byte(dummyClientSecret)})
				// setup chrome and chromedriver
				service, wd := selenium.StartChromeOrFail(ctx)
				defer service.Stop()
				defer wd.Quit()
				// Navigate
				if err := wd.Get("https://localhost:8443/headers"); err != nil {
					ctx.Fatalf("unable to fetch the localhost headers page %v", err)
				}
				// Sign in email page
				if err := wd.WaitWithTimeout(selenium.WaitForTitleCondition("Sign in - Google Accounts"), 20*time.Second); err != nil {
					ctx.Fatalf("unable to load sign in page %v", err)
				}
				selenium.InputByXPathOrFail(ctx, wd, "//*[@id=\"identifierId\"]\n", "cloud_gatekeeper_prober_prod_authorized@gmail.com")
				selenium.ClickByXPathOrFail(ctx, wd, "//*[@id=\"identifierNext\"]/div/button")
				// Password page
				if err := wd.Wait(selenium.GoogleSignInPageIdleCondition()); err != nil {
					ctx.Fatalf("unable to load password page %v", err)
				}
				selenium.InputByCSSOrFail(ctx, wd, "#password input", "bB2iAGl7VfDE7n7")
				selenium.ClickByXPathOrFail(ctx, wd, "//*[@id=\"passwordNext\"]/div/button")
				// Headers page
				if err := wd.WaitWithTimeout(selenium.WaitForElementByXPathCondition("/html/body/pre"), 20*time.Second); err != nil {
					ctx.Fatalf("unable to load headers page %v", err)
				}
				// Get a reference to the text box containing code.
				elem := selenium.FindElementByXPathOrFail(ctx, wd, "/html/body/pre")
				tx, err := elem.Text()
				if err != nil {
					ctx.Fatalf("unable to get the text from headers page content %v", err)
				}
				ctx.Log(tx)
				if !strings.Contains(tx, "Authentication Failed.") {
					ctx.Fatalf("Failed to detect authentication failure.")
				}
			})

			// Client ID misconfiguration should have invalid_client error
			ctx.NewSubTest("invalidClientID").Run(func(ctx framework.TestContext) {
				updateSecretOrFail(ctx, secretName, []string{clientID, clientSecret}, [][]byte{[]byte(dummyClientID), originalClientSecret})
				// setup chrome and chromedriver
				service, wd := selenium.StartChromeOrFail(ctx)
				defer service.Stop()
				defer wd.Quit()
				// Navigate
				if err := wd.Get("https://localhost:8443/headers"); err != nil {
					ctx.Fatalf("unable to fetch the localhost headers page %v", err)
				}
				// Sign in email page
				if err := wd.WaitWithTimeout(selenium.WaitForTitleCondition("Sign in - Google Accounts"), 20*time.Second); err != nil {
					ctx.Fatalf("unable to load sign in page %v", err)
				}
				// Get a reference to the text box containing code.
				elem := selenium.FindElementByXPathOrFail(ctx, wd, "//*[@id=\"view_container\"]/div/div/div[2]/div/div[1]/div/form/span/section/header/div/h2/span")
				tx, err := elem.Text()
				if err != nil {
					ctx.Fatalf("unable to get the text from sign in page content %v", err)
				}
				ctx.Log(tx)
				if !strings.Contains(tx, "Error 401: invalid_client") {
					ctx.Fatalf("Cannot find invalid client id text.")
				}
			})

			// revert oauth secret after test client_id and client_secret
			updateSecretOrFail(ctx, secretName, []string{clientID, clientSecret}, [][]byte{originalClientID, originalClientSecret})

			// No outputJWTAudience field should timeout due to redirection loop
			ctx.NewSubTest("outputJWTAudience").Run(func(ctx framework.TestContext) {
				// Test no outputJWTAudience field
				ctx.ConfigKube(ctx.Clusters().Configs()...).YAML(userAuthNS, util.NoAudUserAuthConfig).ApplyOrFail(ctx)
				time.Sleep(5 * time.Second)
				// setup chrome and chromedriver
				service, wd := selenium.StartChromeOrFail(ctx)
				defer service.Stop()
				defer wd.Quit()
				// Navigate
				if err := wd.Get("https://localhost:8443/headers"); err != nil {
					ctx.Fatalf("unable to fetch the localhost headers page %v", err)
				}
				// Sign in email page
				if err := wd.WaitWithTimeout(selenium.WaitForTitleCondition("Sign in - Google Accounts"), 20*time.Second); err != nil {
					ctx.Fatalf("unable to load sign in page %v", err)
				}
				selenium.InputByXPathOrFail(ctx, wd, "//*[@id=\"identifierId\"]\n", "cloud_gatekeeper_prober_prod_authorized@gmail.com")
				selenium.ClickByXPathOrFail(ctx, wd, "//*[@id=\"identifierNext\"]/div/button")
				// Password page
				if err := wd.Wait(selenium.GoogleSignInPageIdleCondition()); err != nil {
					ctx.Fatalf("unable to load password page %v", err)
				}
				selenium.InputByCSSOrFail(ctx, wd, "#password input", "bB2iAGl7VfDE7n7")
				selenium.ClickByXPathOrFail(ctx, wd, "//*[@id=\"passwordNext\"]/div/button")
				// Timeout for loading error page due to headless mode
				// TODO(b/210551310): revisit test after fixing the behavior
				if err := wd.WaitWithTimeout(selenium.WaitForElementByXPathCondition("//*[@id=\"error-information-popup-content\"]/div[2]"), 30*time.Second); err != nil {
					ctx.Log("unable to load error page %v", err)
				}
			})

			time.Sleep(5 * time.Second)

			// Invalid redirect URL Host should get Error 400: redirect_uri_mismatch
			ctx.NewSubTest("invalidRedirectURLHost").Run(func(ctx framework.TestContext) {
				config := util.NewUserAuthConfig(
					util.UserAuthConfigFields{
						CA:           "",
						IssuerURI:    "https://accounts.google.com",
						Proxy:        "",
						RedirectHost: "https://fake.host",
						RedirectPath: "/_gcp_anthos_callback",
						Scopes:       "",
						GroupsClaim:  "",
						Aud:          "test_audience",
					})
				ctx.ConfigKube(ctx.Clusters().Configs()...).YAML(userAuthNS, config).ApplyOrFail(ctx)
				time.Sleep(5 * time.Second)
				// setup chrome and chromedriver
				service, wd := selenium.StartChromeOrFail(ctx)
				defer service.Stop()
				defer wd.Quit()
				// Navigate
				if err := wd.Get("https://localhost:8443/headers"); err != nil {
					ctx.Fatalf("unable to fetch the localhost headers page %v", err)
				}
				// Sign in email page
				if err := wd.WaitWithTimeout(selenium.WaitForTitleCondition("Sign in - Google Accounts"), 20*time.Second); err != nil {
					ctx.Fatalf("unable to load sign in page %v", err)
				}
				// Get a reference to the text box containing code.
				elem := selenium.FindElementByXPathOrFail(ctx, wd, "//*[@id=\"view_container\"]/div/div/div[2]/div/div[1]/div/form/span/section[1]/header/div/h2/span")
				tx, err := elem.Text()
				if err != nil {
					ctx.Fatalf("unable to get the text from sign in page content %v", err)
				}
				ctx.Log(tx)
				if !strings.Contains(tx, "Error 400: redirect_uri_mismatch") {
					ctx.Fatalf("Cannot find redirect uri mismatch text.")
				}
			})

			time.Sleep(5 * time.Second)

			// Invalid redirect URL Path should get Error 400: redirect_uri_mismatch
			ctx.NewSubTest("invalidRedirectURLPath").Run(func(ctx framework.TestContext) {
				config := util.NewUserAuthConfig(
					util.UserAuthConfigFields{
						CA:           "",
						IssuerURI:    "https://accounts.google.com",
						Proxy:        "",
						RedirectHost: localhostURL,
						RedirectPath: "/dummy",
						Scopes:       "",
						GroupsClaim:  "",
						Aud:          "test_audience",
					})
				ctx.ConfigKube(ctx.Clusters().Configs()...).YAML(userAuthNS, config).ApplyOrFail(ctx)
				time.Sleep(5 * time.Second)
				// setup chrome and chromedriver
				service, wd := selenium.StartChromeOrFail(ctx)
				defer service.Stop()
				defer wd.Quit()
				// Navigate
				if err := wd.Get("https://localhost:8443/headers"); err != nil {
					ctx.Fatalf("unable to fetch the localhost headers page %v", err)
				}
				// Sign in email page
				if err := wd.WaitWithTimeout(selenium.WaitForTitleCondition("Sign in - Google Accounts"), 20*time.Second); err != nil {
					ctx.Fatalf("unable to load sign in page %v", err)
				}
				// Get a reference to the text box containing code.
				elem := selenium.FindElementByXPathOrFail(ctx, wd, "//*[@id=\"view_container\"]/div/div/div[2]/div/div[1]/div/form/span/section[1]/header/div/h2/span")
				tx, err := elem.Text()
				if err != nil {
					ctx.Fatalf("unable to get the text from sign in page content %v", err)
				}
				ctx.Log(tx)
				if !strings.Contains(tx, "Error 400: redirect_uri_mismatch") {
					ctx.Fatalf("Cannot find redirect uri mismatch text.")
				}
			})

			time.Sleep(5 * time.Second)

			// Invalid scope value should get Error 400: Error 400: invalid_scope
			ctx.NewSubTest("invalidScopes").Run(func(ctx framework.TestContext) {
				config := util.NewUserAuthConfig(
					util.UserAuthConfigFields{
						CA:           "",
						IssuerURI:    "https://accounts.google.com",
						Proxy:        "",
						RedirectHost: localhostURL,
						RedirectPath: "/_gcp_anthos_callback",
						Scopes:       "dummy",
						GroupsClaim:  "",
						Aud:          "test_audience",
					})
				ctx.ConfigKube(ctx.Clusters().Configs()...).YAML(userAuthNS, config).ApplyOrFail(ctx)
				time.Sleep(5 * time.Second)
				// setup chrome and chromedriver
				service, wd := selenium.StartChromeOrFail(ctx)
				defer service.Stop()
				defer wd.Quit()
				// Navigate
				if err := wd.Get("https://localhost:8443/headers"); err != nil {
					ctx.Fatalf("unable to fetch the localhost headers page %v", err)
				}
				// Sign in email page
				if err := wd.WaitWithTimeout(selenium.WaitForTitleCondition("Sign in - Google Accounts"), 20*time.Second); err != nil {
					ctx.Fatalf("unable to load sign in page %v", err)
				}
				// Get a reference to the text box containing code.
				elem := selenium.FindElementByXPathOrFail(ctx, wd, "//*[@id=\"view_container\"]/div/div/div[2]/div/div[1]/div/form/span/section/header/div/h2/span")
				tx, err := elem.Text()
				if err != nil {
					ctx.Fatalf("unable to get the text from sign in page content %v", err)
				}
				ctx.Log(tx)
				if !strings.Contains(tx, "Error 400: invalid_scope") {
					ctx.Fatalf("Cannot find invalid_scope text.")
				}
			})

			time.Sleep(5 * time.Second)

			// Invalid IssuerURI should get Authentication Error
			ctx.NewSubTest("invalidIssuerURI").Run(func(ctx framework.TestContext) {
				config := util.NewUserAuthConfig(
					util.UserAuthConfigFields{
						CA:           "",
						IssuerURI:    "https://invalid.issuer.com",
						Proxy:        "",
						RedirectHost: localhostURL,
						RedirectPath: "/_gcp_anthos_callback",
						Scopes:       "",
						GroupsClaim:  "",
						Aud:          "test_audience",
					})
				ctx.ConfigKube(ctx.Clusters().Configs()...).YAML(userAuthNS, config).ApplyOrFail(ctx)
				time.Sleep(5 * time.Second)
				// setup chrome and chromedriver
				service, wd := selenium.StartChromeOrFail(ctx)
				defer service.Stop()
				defer wd.Quit()
				// Navigate
				if err := wd.Get("https://localhost:8443/headers"); err != nil {
					ctx.Fatalf("unable to fetch the localhost headers page %v", err)
				}
				// Headers page
				if err := wd.WaitWithTimeout(selenium.WaitForElementByXPathCondition("/html/body/pre"), 20*time.Second); err != nil {
					ctx.Fatalf("unable to load headers page %v", err)
				}
				// Get a reference to the text box containing code.
				elem := selenium.FindElementByXPathOrFail(ctx, wd, "/html/body/pre")
				tx, err := elem.Text()
				if err != nil {
					ctx.Fatalf("unable to get the text from headers page content %v", err)
				}
				ctx.Log(tx)
				if !strings.Contains(tx, "Authentication Failed.") {
					ctx.Fatalf("Failed to detect authentication failure.")
				}
			})

			time.Sleep(5 * time.Second)

			// Invalid Issuer CA should get Authentication Error
			ctx.NewSubTest("invalidIssuerCA").Run(func(ctx framework.TestContext) {
				config := util.NewUserAuthConfig(
					util.UserAuthConfigFields{
						CA:           "ZmFrZSBjZXJ0aWZpY2F0ZQ==",
						IssuerURI:    "https://accounts.google.com",
						Proxy:        "",
						RedirectHost: localhostURL,
						RedirectPath: "/_gcp_anthos_callback",
						Scopes:       "",
						GroupsClaim:  "",
						Aud:          "test_audience",
					})
				ctx.ConfigKube(ctx.Clusters().Configs()...).YAML(userAuthNS, config).ApplyOrFail(ctx)
				time.Sleep(5 * time.Second)
				// setup chrome and chromedriver
				service, wd := selenium.StartChromeOrFail(ctx)
				defer service.Stop()
				defer wd.Quit()
				// Navigate
				if err := wd.Get("https://localhost:8443/headers"); err != nil {
					ctx.Fatalf("unable to fetch the localhost headers page %v", err)
				}
				// Headers page
				if err := wd.WaitWithTimeout(selenium.WaitForElementByXPathCondition("/html/body/pre"), 20*time.Second); err != nil {
					ctx.Fatalf("unable to load headers page %v", err)
				}
				// Get a reference to the text box containing code.
				elem := selenium.FindElementByXPathOrFail(ctx, wd, "/html/body/pre")
				tx, err := elem.Text()
				if err != nil {
					ctx.Fatalf("unable to get the text from headers page content %v", err)
				}
				ctx.Log(tx)
				if !strings.Contains(tx, "Authentication Failed.") {
					ctx.Fatalf("Failed to detect authentication failure.")
				}
			})

			time.Sleep(5 * time.Second)

			// revert the config back to original
			ctx.ConfigKube(ctx.Clusters().Configs()...).YAML(userAuthNS, util.OriginalUserAuthConfig).ApplyOrFail(ctx)

			forwarder.Close()
		})
}

// updateSecretOrFail updates the K8s secret and restarts the authservice deployment
func updateSecretOrFail(ctx framework.TestContext, secretName string, fields []string, dataList [][]byte) {
	secret, err := secretClient.Get(context.TODO(), secretName, metav1.GetOptions{})
	if err != nil {
		ctx.Fatalf("failed getting oauth secret: %v", err)
	}
	for i := 0; i < len(fields); i++ {
		secret.Data[fields[i]] = dataList[i]
	}
	_, err = secretClient.Update(context.TODO(), secret, metav1.UpdateOptions{})
	if err != nil {
		ctx.Fatalf("failed updating oauth secret: %v", err)
	}
	time.Sleep(10 * time.Second)
	// rollout restart deployment
	util.RestartDeploymentOrFail(ctx, []string{authservice}, userAuthNS)
	time.Sleep(10 * time.Second)
}
