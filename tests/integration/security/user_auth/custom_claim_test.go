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
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/tests/integration/security/user_auth/selenium"
	"istio.io/istio/tests/integration/security/user_auth/util"
)

type rcTokenClaim struct {
	Attribute interface{} `json:"attributes"`
	Aud       string      `json:"aud"`
	Sub       string      `json:"sub"`
}

func TestCustomJwtClaim(t *testing.T) {
	framework.
		NewTest(t).
		Features("security.user.auth").
		Run(func(ctx framework.TestContext) {
			util.SetupConfig(ctx)
			util.ApplyUserAuthConfigIfNotExist(ctx)
			time.Sleep(5 * time.Second)

			// port-forward to cluster
			forwarder := util.GetIngressPortForwarderOrFail(ctx, ist, localPort, ingressPort)
			if err := forwarder.Start(); err != nil {
				t.Fatalf("failed starting port forwarder for pod: %v", err)
			}
			// check the port-forward availability
			util.ValidatePortForward(ctx, strconv.Itoa(localPort))

			config := util.CustomClaimUserAuthConfig
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
			var rcTokenString string
			var rcClaim rcTokenClaim
			expectedTestAud := "1042451928015-7voa7j6bvjp28eqcfhj5dvhbp6n248ip.apps.googleusercontent.com"
			// RCToken will always be present in field X-Asm-Rctoken,
			// checking string between " " which will always be our token
			// TODO(garyan) : Add a better logic other than string manipulation to handle this scenario
			index := strings.Index(tx, "X-Asm-Rctoken")
			tokenSubstring := tx[index+1:]
			needSecondOccurence := 0
			for _, ch := range tokenSubstring {
				if string(ch) == `"` {
					needSecondOccurence++
				}
				if needSecondOccurence == 2 && string(ch) != `"` {
					rcTokenString += string(ch)
				}
			}

			if rcClaim, err = parseClaims(rcTokenString); err != nil {
				ctx.Fatalf("failed to extract claims from ID token: %v", err)
			}
			_, exist := rcClaim.Attribute.(map[string]interface{})["invalid_claim"]
			if exist {
				ctx.Fatalf("Invalid claim is present in the token")
			}

			val, exist := rcClaim.Attribute.(map[string]interface{})["test_aud"].(string)
			if !exist {
				ctx.Fatalf("test_aud claim is not present in the token")
			} else if val != expectedTestAud {
				ctx.Fatalf("Wrong value present in claim test_aud")
			}

			val, exist = rcClaim.Attribute.(map[string]interface{})["test_decision"].(string)
			if !exist {
				ctx.Fatalf("test_decision claim is not present in the token")
			} else if val != "Positive" {
				ctx.Fatalf("Wrong value present in claim test_decision")
			}

			forwarder.Close()
			time.Sleep(5 * time.Second)
		})
}

func parseClaims(token string) (rcTokenClaim, error) {
	var claims rcTokenClaim
	parts := strings.Split(token, ".")

	// token contains 3 parts : header, payload, signature, we only want the 2nd part (payload)
	if len(parts) != 3 {
		return claims, fmt.Errorf("token contains an invalid number of segments: %d, expected: 3", len(parts))
	}

	// Decode the second part.
	claimBytes, err := decodeSegment(parts[1])
	if err != nil {
		return claims, err
	}
	dec := json.NewDecoder(bytes.NewBuffer(claimBytes))

	if err := dec.Decode(&claims); err != nil {
		return claims, fmt.Errorf("failed to decode the JWT claims")
	}
	return claims, nil
}

// Decode JWT specific base64url encoding with padding stripped
func decodeSegment(seg string) ([]byte, error) {
	if l := len(seg) % 4; l > 0 {
		seg += strings.Repeat("=", 4-l)
	}

	return base64.URLEncoding.DecodeString(seg)
}
