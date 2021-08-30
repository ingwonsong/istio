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
	port              = 8443
	clientID          = "clientID"
	clientSecret      = "clientSecret"
	dummyClientID     = "dGVzdGNsaWVudGlk"
	dummyClientSecret = "dGVzdGNsaWVudHNlY3JldA=="
	authservice       = "authservice"
)

var secretClient corev1.SecretInterface

// TestMisconfiguration Test following misconfigurations for User Auth:
// 1. Client ID in OAuth secret
// 2. Client Secret in OAuth secret
// 3. Redirect URL in UserAuthConfig
// 4. Issuer URL in UserAuthConfig
func TestMisconfiguration(t *testing.T) {
	framework.
		NewTest(t).
		RequiresSingleCluster().
		Features("security.user.auth").
		Run(func(ctx framework.TestContext) {
			util.SetupConfig(ctx)
			// port-forward to cluster
			forwarder := util.GetIngressPortForwarderOrFail(ctx, ist, port, port)
			if err := forwarder.Start(); err != nil {
				t.Fatalf("failed starting port forwarder for ingress: %v", err)
			}
			// check the port-forward availability
			util.ValidatePortForward(ctx, strconv.Itoa(port))

			// Get clients
			cluster := ctx.Clusters().Default()
			secretClient = cluster.CoreV1().Secrets(userAuthNS)
			oauthSecret, err := secretClient.Get(context.TODO(), secretName, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("failed getting oauth secret: %v", err)
			}
			originalClientID := oauthSecret.Data[clientID]
			originalClientSecret := oauthSecret.Data[clientSecret]

			// Client secret misconfiguration should fail authentication
			ctx.NewSubTest("clientSecret").Run(func(ctx framework.TestContext) {
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
			ctx.NewSubTest("clientID").Run(func(ctx framework.TestContext) {
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
			forwarder.Close()
		})
}

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
