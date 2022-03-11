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
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/shell"
	"istio.io/istio/tests/integration/security/user_auth/selenium"
	"istio.io/istio/tests/integration/security/user_auth/util"
)

// proxyCA is the base64 encoded value from squid configmap located in prow/asm/tester/configs/user-auth/squid.yaml
const (
	proxyPort     = 3128
	proxyMITMPort = 3129
	proxyNS       = "squid"
	proxyCA       = "LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSUVoakNDQTI2Z0F3SUJBZ0lKQUlOaVp0bGJUcWQwTUEwR0NTcUdTSWIzRFFFQkN3VUFNSE14Q3pBSkJnTlYKQkFZVEFrbFVNUTR3REFZRFZRUUlEQVZKZEdGc2VURU9NQXdHQTFVRUJ3d0ZUV2xzWVc0eERqQU1CZ05WQkFvTQpCVk54ZFdsa01ROHdEUVlEVlFRTERBWkVaWFpQY0hNeEl6QWhCZ05WQkFNTUdsTnhkV2xrSUd0MVltVnlibVYwClpYTXVhVzhnWm1sc2RHVnlNQjRYRFRJeE1USXlNakl3TURFMU9Gb1hEVE14TVRJeU1ESXdNREUxT0Zvd2N6RUwKTUFrR0ExVUVCaE1DU1ZReERqQU1CZ05WQkFnTUJVbDBZV3g1TVE0d0RBWURWUVFIREFWTmFXeGhiakVPTUF3RwpBMVVFQ2d3RlUzRjFhV1F4RHpBTkJnTlZCQXNNQmtSbGRrOXdjekVqTUNFR0ExVUVBd3dhVTNGMWFXUWdhM1ZpClpYSnVaWFJsY3k1cGJ5Qm1hV3gwWlhJd2dnRWlNQTBHQ1NxR1NJYjNEUUVCQVFVQUE0SUJEd0F3Z2dFS0FvSUIKQVFDc2Z5Zy9kTXBjZ1BMUjVycW5nMm1BQkFBaGF3TWlNY3kxU1NCSlQyQldYUmRrSVpnY2JXOTFackI0eXJXbApLY01iUVZTbFQrQVJBbUFmdDVjWG1yQW5IQTVTOGZsZFJKdHp2cGRsZUFBb3FLUks1YmNpMEo3MnlqVElEdmhoCjJmSjE5anEzMUh1Vm5ldW12ejIvR0xnQURRZjBtNUpSaitQenVzdXZQYUc4NUU5VGlGcjYveVZpd2FKVnkzakQKZVpDSTZHVlBXYy9CamNNZGpGZzJHVEZ6a3RnOEc4c0FhMGpoUytzZDBDYlY2OE1IUlR4SGdkMmxkM1dLdjk2NQpKeTRkQWhoUTF0ZGZ0NEJlZkpSdFAyTTgxcVFpK25POFpsZ3FHTFJuWkc3eWZ3RjQrNzdOWTArZ0dhRGVabnNrClBSRDhrWW92M2FFNXVvQVZGYXBIZmVUVEFnTUJBQUdqZ2dFYk1JSUJGekFkQmdOVkhRNEVGZ1FVUWhpc01JWlUKUUd3TjY3dTRMMnYxOFFnM2NVMHdnYVVHQTFVZEl3U0JuVENCbW9BVVFoaXNNSVpVUUd3TjY3dTRMMnYxOFFnMwpjVTJoZDZSMU1ITXhDekFKQmdOVkJBWVRBa2xVTVE0d0RBWURWUVFJREFWSmRHRnNlVEVPTUF3R0ExVUVCd3dGClRXbHNZVzR4RGpBTUJnTlZCQW9NQlZOeGRXbGtNUTh3RFFZRFZRUUxEQVpFWlhaUGNITXhJekFoQmdOVkJBTU0KR2xOeGRXbGtJR3QxWW1WeWJtVjBaWE11YVc4Z1ptbHNkR1Z5Z2drQWcySm0yVnRPcDNRd0RBWURWUjBUQkFVdwpBd0VCL3pBZkJnTlZIUkVFR0RBV2dSUnliMjkwUUhOeGRXbGtMV05oWTJobExtOXlaekFmQmdOVkhSSUVHREFXCmdSUnliMjkwUUhOeGRXbGtMV05oWTJobExtOXlaekFOQmdrcWhraUc5dzBCQVFzRkFBT0NBUUVBSUZnaGV4YjkKRjZqU0d6cmVGZitZZzZnK2ZodWlvRFpOeW92V2U4T3gzTGw1WnR6VGp0QTM3QXd0L2JmeGtoSlI3MzBVRVBxWApBakJKalRQbU41dFl6eDJzajg5YzlCb1UvNVU5bFh5NmlpMDQ3Rk9MRjhoSXNTMVFuM3IyL0lXUlpYOGgzdDFECk1MMGZOMFBlMnVoMkdrQXAvRHJjaGk2cldlK0tiMUp3T2F3RU1QMGY1akhzSEprdU9GRDdDNk1McnlJYk5MMUUKRWlhdUw4YnJhZmpSb1ZyMHJNcHIzdDZUSFNWbzNUMzc0K3k4NTdHSUs0RGZVSEdDUWV3YXhFWFBGbzRHN292KwovN29hOWVVVXRmSFBNandWb2hwdEplekxBN0tIR2FBUnRtZkM1YTZGZjVVdUtEajBORDVFWG1mcTc1c09jUUo4CnN2blArZkwrcHRkTmVnPT0KLS0tLS1FTkQgQ0VSVElGSUNBVEUtLS0tLQ==" // nolint: lll
)

var proxyHost string

// TestProxy tests following cases
// 1. Use normal HTTP proxy and IDP's root cert, user should be able to finish authn flow
// 2. Use normal HTTP proxy and invalid IDP's cert, user should fail authn flow
// 3. Use MITM proxy and proxy's cert, user should be able to finish authn flow
// 4. Use MITM proxy and IDP's root cert, user should fail authn flow
func TestProxy(t *testing.T) {
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

			var err error
			if proxyHost, err = getProxyHost(); err != nil {
				t.Fatalf("failed to get squid proxy URL: %v", err)
			}

			// validProxy setup should finish the OIDC authn flow successfully
			ctx.NewSubTest("validProxy").Run(func(ctx framework.TestContext) {
				config := util.NewUserAuthConfig(
					util.UserAuthConfigFields{
						CA:           util.GoogleIDPRootCA,
						IssuerURI:    "https://accounts.google.com",
						Proxy:        fmt.Sprintf("%s:%d", proxyHost, proxyPort),
						RedirectHost: localhostURL,
						RedirectPath: "/_gcp_anthos_callback",
						Scopes:       "",
						GroupsClaim:  "",
						Aud:          "test_audience",
					})
				ctx.ConfigKube(ctx.Clusters().Configs()...).YAML(config).ApplyOrFail(ctx, userAuthNS)
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
				if !strings.Contains(tx, "X-Asm-Rctoken") {
					ctx.Fatalf("X-Asm-Rctoken is not in the header")
				}
			})

			time.Sleep(5 * time.Second)

			// invalidIDPCA should get authentication error
			ctx.NewSubTest("invalidIDPCA").Run(func(ctx framework.TestContext) {
				config := util.NewUserAuthConfig(
					util.UserAuthConfigFields{
						CA:           proxyCA,
						IssuerURI:    "https://accounts.google.com",
						Proxy:        fmt.Sprintf("%s:%d", proxyHost, proxyPort),
						RedirectHost: localhostURL,
						RedirectPath: "/_gcp_anthos_callback",
						Scopes:       "",
						GroupsClaim:  "",
						Aud:          "test_audience",
					})
				ctx.ConfigKube(ctx.Clusters().Configs()...).YAML(config).ApplyOrFail(ctx, userAuthNS)
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

			// validMITMProxy setup should finish the OIDC authn flow successfully
			ctx.NewSubTest("validMITMProxy").Run(func(ctx framework.TestContext) {
				config := util.NewUserAuthConfig(
					util.UserAuthConfigFields{
						CA:           proxyCA,
						IssuerURI:    "https://accounts.google.com",
						Proxy:        fmt.Sprintf("%s:%d", proxyHost, proxyMITMPort),
						RedirectHost: localhostURL,
						RedirectPath: "/_gcp_anthos_callback",
						Scopes:       "",
						GroupsClaim:  "",
						Aud:          "test_audience",
					})
				ctx.ConfigKube(ctx.Clusters().Configs()...).YAML(config).ApplyOrFail(ctx, userAuthNS)
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
				if !strings.Contains(tx, "X-Asm-Rctoken") {
					ctx.Fatalf("X-Asm-Rctoken is not in the header")
				}
			})

			time.Sleep(5 * time.Second)

			// invalidMITMProxyCA should get authentication error
			ctx.NewSubTest("invalidMITMProxyCA").Run(func(ctx framework.TestContext) {
				config := util.NewUserAuthConfig(
					util.UserAuthConfigFields{
						CA:           util.GoogleIDPRootCA,
						IssuerURI:    "https://accounts.google.com",
						Proxy:        fmt.Sprintf("%s:%d", proxyHost, proxyMITMPort),
						RedirectHost: localhostURL,
						RedirectPath: "/_gcp_anthos_callback",
						Scopes:       "",
						GroupsClaim:  "",
						Aud:          "test_audience",
					})
				ctx.ConfigKube(ctx.Clusters().Configs()...).YAML(config).ApplyOrFail(ctx, userAuthNS)
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

			forwarder.Close()
		})
}

// getProxyHost fetches the squid proxy host address from cluster
func getProxyHost() (string, error) {
	getProxyIPCmd := fmt.Sprintf("kubectl get pod -n %s -l app=squid -o jsonpath={.items..podIP}", proxyNS)
	proxyIP, err := shell.Execute(true, getProxyIPCmd)
	var proxyURL string
	if err == nil {
		proxyURL = fmt.Sprintf("http://%s", proxyIP)
	}
	return proxyURL, err
}
