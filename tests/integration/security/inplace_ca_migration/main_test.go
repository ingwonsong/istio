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

package inplacecamigration

import (
	"fmt"
	"os"
	"testing"

	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/deployment"
	"istio.io/istio/pkg/test/framework/components/echo/match"
	"istio.io/istio/pkg/test/framework/components/istio"
	"istio.io/istio/pkg/test/framework/components/namespace"
	"istio.io/istio/pkg/test/framework/label"
	"istio.io/istio/pkg/test/framework/resource"
	"istio.io/istio/tests/integration/security/util"
)

const (
	ASvc              = "a"
	BSvc              = "b"
	defaultCAPool     = "projects/asm-prow-build/locations/us-central1/caPools/asm-testci-sub-pool-us-central1"
	meshCATrustAnchor = `-----BEGIN CERTIFICATE-----
MIIGlDCCBHygAwIBAgIQEW25APa7S9Sj/Nj6V6GxQTANBgkqhkiG9w0BAQsFADCB
wTELMAkGA1UEBhMCVVMxEzARBgNVBAgTCkNhbGlmb3JuaWExFjAUBgNVBAcTDU1v
dW50YWluIFZpZXcxEzARBgNVBAoTCkdvb2dsZSBMTEMxDjAMBgNVBAsTBUNsb3Vk
MWAwXgYDVQQDDFdpc3Rpb192MV9jbG91ZF93b3JrbG9hZF9yb290LXNpZ25lci0w
LTIwMTgtMDQtMjVUMTQ6MTE6MzMtMDc6MDAgSzoxLCAxOkg1MnZnd0VtM3RjOjA6
MTgwIBcNMTgwNDI1MjExMTMzWhgPMjExODA0MjUyMjExMzNaMIHBMQswCQYDVQQG
EwJVUzETMBEGA1UECBMKQ2FsaWZvcm5pYTEWMBQGA1UEBxMNTW91bnRhaW4gVmll
dzETMBEGA1UEChMKR29vZ2xlIExMQzEOMAwGA1UECxMFQ2xvdWQxYDBeBgNVBAMM
V2lzdGlvX3YxX2Nsb3VkX3dvcmtsb2FkX3Jvb3Qtc2lnbmVyLTAtMjAxOC0wNC0y
NVQxNDoxMTozMy0wNzowMCBLOjEsIDE6SDUydmd3RW0zdGM6MDoxODCCAiIwDQYJ
KoZIhvcNAQEBBQADggIPADCCAgoCggIBAK9dFtiHI0/r70k6WhbEuDgHDzl/O5MP
symbiYF4cQ4ZDkMgXT2aVHyuB/MmdqteC2spuG5ojC6HCHhj+9JFF3KfU+Ej/j9A
5gUz8d4VYD92LXh82irI2tOelbQ7PZALn5hRnpk0gnpzLe2kpa8lGy0TtB/v/303
jZAl0e7iKKkX1Ooy9JzeBAYdNqu9uIhrl+naSXEGpuWxCY6uip+EcuKp9cR16kFz
OKNn+sQ09d7KeFN9rLaZiodBzndVliq9FkWJwIeBp06Cq9tUMkmYvUFk3mt86nhZ
rYviLI4tXSmZIGQNkvPQXU1ki3ukv/ZVdJrXV7w2k/EZmiF+9oxPwG4Z21GFfOKT
WL/k9i7SCqIuT41pNK2mmKHsQI7Qlortz54s8Y2DpzcKc4EDfUIf62No7++HIMxu
dPxbdQe11ImpDRcQIg9ZOqTboruLaGNBLO6rdcnqmgts3CLrlex1L9QGxQZCHRea
riR1bWeQBNTAodYqSz6vpSI5hXUCkkXW7LFcPMqvRbRVclak8/Rp0LaHLQiDxkKp
iQ1prfBO4IxYcHlEKPnxoBrk5WUZfX4j0Opbj8hkNVi+sB+/RvXruFLZoFAzcKu5
KWKtPzUOb8P9VkEmNy7D3JXys5Gfi8NkVXn/khVVb1BTHHf9wC3nhJ50nuBkmPu7
3bj1ABOajB+zAgMBAAGjgYMwgYAwDgYDVR0PAQH/BAQDAgEGMB0GA1UdJQQWMBQG
CCsGAQUFBwMBBggrBgEFBQcDAjAPBgNVHRMBAf8EBTADAQH/MB0GA1UdDgQWBBQ/
VsuyjgRDAEmcZjyJ77619Js9ijAfBgNVHSMEGDAWgBQ/VsuyjgRDAEmcZjyJ7761
9Js9ijANBgkqhkiG9w0BAQsFAAOCAgEAUc5QJOqxmMJY0E2rcHEWQYRah1vat3wu
IHtEZ3SkSumyj+y9eyIHb9XTTyc4SyGyX1n8Rary8oSgQV4cbyJTFXEEQOGLHB9/
98EKThgJtfPsos2WKe/59S8yN05onpxcaL9y4S295Kv9kcSQxLm5UfjlqsKeHJZy
mvxiYzmBox7LA1zqcLYZvslJNkJxKAk5JA66iyDSQqOK7jIixn8pi305dFGCZglU
FStwWqY6Rc9rR8EycVhSx2AhrvT7OQTVdKLfoKA84D8JZJPB7hrxqKf7JJFs87Kj
t7c/5bXPFJ2osmjoNYnbHjiq64bh20sSCd630qvhhePLwjjOlBPiFyK36o/hQN87
1AEm1SCHy+aQcfJqF5KTgPnZQy5D+D/CGau+BfkO+WCGDVxRleYBJ4g2NbATolyg
B2KWXrj07U/WaWqV2hERbkmxXFh6cUdlkX2MeoG4v6ZD2OKAPx5DpJCfp0TEq6Pz
nP+Z1mLd/ZjGsOF8R2WGQJEuU8HRzvsr0wsX9UyLMqf5XViDK11V/W+dcIvjHCay
BpX2se3dfex5jFht+JcQc+iwB8caSXkR6tGSiargEtSJODORacO9IB8b6W8Sm//J
Wf/8zyiCcMm1i2yVVphwE1kczFwunAh0JB896VaXGVxXeKEAMQoXHjgDdCYp8/Et
xjb8UkCmyjU=
-----END CERTIFICATE-----`

	// TODO(shankgan): this corresponds to the trustAnchors of ca-pools
	// in the asm-prow-build gcp project. need to retrieve this as env variable?
	privateCATrustAnchor = `-----BEGIN CERTIFICATE-----
MIIFWjCCA0KgAwIBAgITQ98gzWSGpf9AS6eM6goWiqp2/jANBgkqhkiG9w0BAQsF
ADA1MRQwEgYDVQQKEwtBU00tVEVTVC1DSTEdMBsGA1UEAxMUYXNtLXRlc3RjaS1y
b290LWNhLTIwHhcNMjMwMzAxMjA0NjEzWhcNMzMwMzAxMDY1MzUzWjA1MRQwEgYD
VQQKEwtBU00tVEVTVC1DSTEdMBsGA1UEAxMUYXNtLXRlc3RjaS1yb290LWNhLTIw
ggIiMA0GCSqGSIb3DQEBAQUAA4ICDwAwggIKAoICAQCgagTHlcvVEb//ej7cTCSd
AfNVPW49RlwnPdiYiRmH6KGviJYLXoNDJYttKPoEXahUjJYs3fEZ2pKFcWIyN5CP
CHG9BSg7G0W7U4Ba76zwfW4gTVoG8iVE8LDhbxD6owoGXVdA4yPOgbUEgO7pu/yn
7Wr+1PdzJXz571245qFVyIWo0iDocon9Qr5Ig06V97goquhZxAtfle5fgiL7Mf9j
B7UAz1b1JiW0L9bASaN47KJ/ttAGejwMwdiZB0OMO8HEi3OtGqiSls+AwN8BpMVk
sA8gGe5Cgmo6ujoMo/RCc10JhQCO4a5ldg1hQJmuCPGo5hIf9cBYQggurPCqnxrE
Nj0Ttn/BLR6xeWbE3neBRwViv7VZDbzCJJqRUzBT5ZdHRjXfD+Duh913oNrVo0ps
bLBdr7rn5YIlOGFow3WKNO2+JQD384P/akmGlpieFROgR3eqSfGv9fUd7Zdo2mhc
u7sI6HeALh/NYmdJxmUco7UyLIN1wsvFNfAl6HHnUZkJnCXYeFMK9SWOqdJRA3h0
/Zxhw5D/AwWeo6uZUiJfkgJNcXq1ImDReWIkufWq6v5ezpWMo0dnJaPQseG/d+v1
39TNmi5wOddSOG6YLU5VeQE/URNTyXPwZ0huKt6OhPJxtXR2tH8jjA8Nd7o/M1Z+
C3mp/AI+USnCqI03fey+BwIDAQABo2MwYTAOBgNVHQ8BAf8EBAMCAQYwDwYDVR0T
AQH/BAUwAwEB/zAdBgNVHQ4EFgQUw9DS1/ydtgZ+Xy+KOAgCgVMyxbQwHwYDVR0j
BBgwFoAUw9DS1/ydtgZ+Xy+KOAgCgVMyxbQwDQYJKoZIhvcNAQELBQADggIBAFok
iQp5xWX6qC86pI5JlPdoOvF4QuH53CQesBdLEskjDq+SRCKGcGVWqOMAH3Yecchq
njE8Fa7AYEet8H6++7twlBAc7hof3Os5DXvP9BRfrGdITfhiEneiLdcvZVdJTwz0
vcacYWJXd+dfKlIkuEgIdO6MHlE0FXYjpgg3er9nctm0z8OTpEmo+1KkMFQK1sBC
i8p9p0SwomnZZWO2eP+UYMKjv0Tg5lvU9InyP4/id93bN3Dst9L9fHxf9FY7q7oM
m1eTzAnBPluxEv8O0eSCoC30uiH0AJEHDeo7dVWKO7SuhiwYQYBrogDfTHn5WNot
IiUQC0Oh7+e2k5q6XOjZkjOLpUSqPHJGLEXn4seSHg8dysgEKGamhKInAtf6w6cK
47uq3mrYpqeuycSaTbzfV2zZ3sDNGkUzqfdb8raGWS5kF9zmvdUYRVA8FenKJTDz
zA0+iq9npkaBSF3B2/O0RfcliZ5t5C6EAwo3Y+gh4lqOMLDULi6ASPQHgoN664db
B7iCt8BMHTsCS3a3ZiUIvrHv4HeUdxc/Ph9UV8TcaDj344BE6pUALBopQ3cAhrqC
sTghRt/hvSFXYnBnCBUC9RHbEGwR+o7lT98LUngMY2DVEqr6Ou8ENk8ICVaP5/YC
TOxGAnbrrwBfmAzqb+joD3RCeJCAerBeCVBH/KL+
-----END CERTIFICATE-----
-----BEGIN CERTIFICATE-----
MIIFWzCCA0OgAwIBAgIUAIRVlqq5PRKDtnftFhD51zKs6rAwDQYJKoZIhvcNAQEL
BQAwNTEUMBIGA1UEChMLQVNNLVRFU1QtQ0kxHTAbBgNVBAMTFGFzbS10ZXN0Y2kt
cm9vdC1jYS0zMB4XDTIzMDMwMTIwNDYyMVoXDTMzMDMwMTA2NTQwMVowNTEUMBIG
A1UEChMLQVNNLVRFU1QtQ0kxHTAbBgNVBAMTFGFzbS10ZXN0Y2ktcm9vdC1jYS0z
MIICIjANBgkqhkiG9w0BAQEFAAOCAg8AMIICCgKCAgEAh7J5+nLJqBBLwYtBvnEI
Dj3lkYAWluqyj/wFhX3Z3UkpYA/qzg2r3mE4gQr9S4ZN6Q+weEJqQf/IVFdHAKFZ
JfkjLCNHmwbQcnfa8lm8Kjdzd24JsmXi3yMyCaZM8xi7cEJ+8C0oPntr+qbNN/F8
IE5shiw44vadpoG88E6qb6Plb5xkSVjDS2uBy6BUrvpbWj4/ncrrltfMH+YrxCyW
Bao3pqLv0Kq58gOSPhcZT+FSLqbN/8zJ5eH4DzTDh1F9RjAAK5pkZPq9MFk6Cudh
QFzCpXjeTDZPOwvU0RVa/I5AQfqxTVidjK4GFmi0m8z3IGM7huTH+UNCpEvcaEsv
aKzqjl7gfSMaMXsP74XHzfJn/7+CXRdfThUuWT/oTNARyE/9P4Pvgu4iIG88rYIJ
t4EsN7vBDkUDn4YzMw6bUXn6BxkLVKu+31xHiO1WVRVI/BIoJz1eBX/hPq5z0hrr
QB3K5IYtQpY9uQNGxHv2FrviQdJrU+6EnEwRTfgDyk5BHoQzxDAd/kbaur7d1UuT
hOjkJKJKmVfn+Yb9UgRW1MsCkgrx2+QndVmdIgkJrMFcL/FcDMBdiCmkG2VFSZ1t
zOCfNZJdJCHx/JoolB4JhJYw4sYoGq1YfwDs/NUV4LNmXponTdxAyWfABe2z2PNn
x86hIpLecarjpUYgpFtq3Q0CAwEAAaNjMGEwDgYDVR0PAQH/BAQDAgEGMA8GA1Ud
EwEB/wQFMAMBAf8wHQYDVR0OBBYEFG7mThmVKeP1+WIpLC36p/V2J841MB8GA1Ud
IwQYMBaAFG7mThmVKeP1+WIpLC36p/V2J841MA0GCSqGSIb3DQEBCwUAA4ICAQAr
QZmOTxVa0Pc6Oz2CKX9esOIrJ/8yysefqDidOWwg1i38/9jm9tfHYfaA+1Uf6ocM
OXuBxt8Lpu4PHiS6Dm0QZPJkXm9akjeMnFVJnE245ArbXkqAddhJ2Z09EajCt+OH
caFAkpbOhjYK9l0eAUgNVVoWIaWaw+b9h6Q1dBnUTVrkBC66RNJC4bORkZjmUwH3
jt/NZm89We++Dz7s+KsV6QKwnU/+6OACRp+zklYIgPE+2/MSHqbdwUJdB8CA6OXc
doDJZWplvMnw6W8tF5qE+Hb9yPWkW+kTyWlayxlANhb/vOmIKRi4i4euyTimV2dR
c/aORLJtaFiV5Oh7Hn/EzjYpxIad1OqLn7SlUtwHFWYPoKk4IEQ+HiEQUA5YVqL0
Bn86HRS3v3TDwSBTu6dSXN+9NKyGFVQ+rWmikPG89TElFYzxVEnO/VfJHZ+oYYWF
ABGfM8isQoLtck7pfB/lSBhruElSRVWLQcuDxlASF9QMxCDIfxQZFv7zaGY3TI9g
BX+mxTiOWuzZaMcHG5wtdCAu8HmNwdZq7Cg1Ov85UsxMSdaEaZR1ZHQzRzwgupkM
hmMj/TuUW2fczXWNbX3AZ0I1Jcgqz6Q6OCX7/FTmLXXp9wVBBD0PEuV3kzoc4mpb
843MOrtXEdryZoyNgyjqRa2vaNVUhavnucrDUXyKoQ==
-----END CERTIFICATE-----
-----BEGIN CERTIFICATE-----
MIIFWjCCA0KgAwIBAgITX1u1uS8b4BSw3ETnMLlbjtGl/DANBgkqhkiG9w0BAQsF
ADA1MRQwEgYDVQQKEwtBU00tVEVTVC1DSTEdMBsGA1UEAxMUYXNtLXRlc3RjaS1y
b290LWNhLTEwHhcNMjMwMzAxMjA0NjAzWhcNMzMwMzAxMDY1MzQzWjA1MRQwEgYD
VQQKEwtBU00tVEVTVC1DSTEdMBsGA1UEAxMUYXNtLXRlc3RjaS1yb290LWNhLTEw
ggIiMA0GCSqGSIb3DQEBAQUAA4ICDwAwggIKAoICAQCeBm47yV83EZPjGP3ZwV6I
jUQwsm1GxwIoZUPAdyF8FyjYclXifImLH3+pz8AwC3HIw+A1lxUr+QCfW9DB96Ys
tjLhTKmbzmwcBduFZh0u2Th5osvXED9yi1EbDigPhS0nBkm52cJ8UNtS3y/JW1Vy
cAqg+7PdTTLF7KkRIr3uLFquKuUd/gPULp4buEFad5jRvU3vbnUJMfzENWYXrAST
p1kF+XtTnG1hzgGdzD/9OUPLtzY+vbpkW9DHFR8WGmLTGWcBNRSb1imDOs4/nrVQ
B6k6bvrhY0tIY1a84eAvdQB8KfEHWFY8ctxqVh//QLOK8fFjIIuvJvTIgvZPa54q
6aTfFZFonMNVjEIdWChSi7r9tH3s6xPdWO5AS5TSVKEfZ6Xgz6kvmuvYdBW20+US
ZV8QGIJwzk4rKvWQAe/1aAYNinOxaaw40iiMRIyVoU0HWlaT7MntGZM/7s7m/2h/
RGNKK98Dz1n6kM21JsZizZNxT8OTXFsuBG7ctq0nMtBujNiYOfihcSjOq2X21Jdv
hsJQ6a6q1iVyODcfmwy1Z/+TKqW3A2C/aapnFvrPPLSdW2dQq1LjdeaJsOhEYwn1
T58EIIEtXjq2ePeuFKpzRBgwpudKwJNEssqNCLppE52r+KAE3Z09RZKaAB+sHuVU
/SiRCbchEwi/vBDzSPWStwIDAQABo2MwYTAOBgNVHQ8BAf8EBAMCAQYwDwYDVR0T
AQH/BAUwAwEB/zAdBgNVHQ4EFgQUQchQvayV1YN7x3nfkyjB0r2k4BwwHwYDVR0j
BBgwFoAUQchQvayV1YN7x3nfkyjB0r2k4BwwDQYJKoZIhvcNAQELBQADggIBAH1T
Dzk9USqrW/l/5WPm/HcSzA+B29TWxhBOX21iCc25GNUxaQUHrVbq60hWuOTGuctG
cWspPcbaWEE02+RrKU8Vak32VCxGWwr95lNY/NVxZ5XHxbyO1N/Gd9BK0THO15oA
b8NgOwHMQt1rFwJwIk/12o8Idebmmnw1r1QJaOVS4hm7XR6sfm/l8LxxntG2jWM8
u7IV7v2lqMClQzCq39cHx+luqNHEbRygX4kLsgEadwWw01dw/lF3/8GKXVb1HW0S
wRndCuDBcnrPe4ov1u6ikyT9sF7MUoEUDY1q0iBY/5Pcs0KBqnycXDUPeOQ5B3r1
NYhp9mVcZ/Z6gRvn72pUrwYIZTXpK63lmdMwW2AFvOimiCZAlAgYpEXUWizYSjXw
uJ7BRzTLAzocipqfFykRMtWTKiqptVzwG9un5QYd8b63nL5HS86UDxCngf12psLC
SMhOkhq+bVyHdDtastZVb5YHRTv3A5g8d+++kjHPPFL7hdqNm1tiAWYSSLExso92
0FGiVW02doejET/Ur/5AjMdf5SPrLAmwLQHirsA6xVK1LGthic18TX3OYKBiNpVf
rNrrqvWQ1Ep+g+/iuOKmKLhN25kWfZLR47EFsQUsCi2BhdlqnIy512FO7EcKdeqL
kUZbjBMahEdsNrOqBBjS8EVV1z9IBXEk1uZ0BHaB
-----END CERTIFICATE-----
-----BEGIN CERTIFICATE-----
MIIFWzCCA0OgAwIBAgIUANLn5LYjeKpk9apA/ra93poIsbYwDQYJKoZIhvcNAQEL
BQAwNTEUMBIGA1UEChMLQVNNLVRFU1QtQ0kxHTAbBgNVBAMTFGFzbS10ZXN0Y2kt
cm9vdC1jYS00MB4XDTIzMDMwMTIwNDYyOVoXDTMzMDMwMTA2NTQwOVowNTEUMBIG
A1UEChMLQVNNLVRFU1QtQ0kxHTAbBgNVBAMTFGFzbS10ZXN0Y2ktcm9vdC1jYS00
MIICIjANBgkqhkiG9w0BAQEFAAOCAg8AMIICCgKCAgEAo7+oJs3XwFbNakiPGhxT
JTV3fxCyM/JacVOpGAv/uqy0cZF83uSUymIzdlMiMWDaJ+AwBFlNjxKT/+xoTVth
oL1KJvl7yWfnmru0mG78SuQQihKJzJffnVbkbJ/845iLx1CdczPTQ9dS5cqibKS5
poeASR/NQ85/3mOs5dse/2Gk1P8woMlcC+jjhcj//wxReRmYnlDcoyVCLcu5aTh6
DckKrJimsKdiTciBHKO2xOIubEhTF+Y5n9UAd9o9KwVq7RasiKrFLFVHzjSWTbkK
oSDnoi7T5jq7IDGgmUSh+NUmg/ymvSfT7a53i05R4G1o5GPADfCEmkjkmVabsrKz
7yOQpUnY6JLQpY7SmKfvShJHi1Q0/ymWiAlPwR9m60BxwwZRDtJB5h95g1B8oGhF
980XmWVcyijwr+hdb9qGz0+LBfMW7NtvnymW3zsGWj8ILjX5CJJhBs5xnCcIFdzg
WHgxlnrNDKXySKBKaoQyJothxV2Jxi54mMvnPUd8Ba9Q5ny8qGT8EdZjG2hASIHc
uHkSRuc3HA+8WOOTwi5iu6E0zGabIzAFts1SO9GzdY/4L6uqSt4/s84r/S5xCdwj
uXX5F3WQJq3zOhXEBIGi96+EmZ8MndMi7Km1xNMbsDvsjXHcTu327qZQgxHz0tTj
ADk3kdPDf8AzSaVjNnu9f18CAwEAAaNjMGEwDgYDVR0PAQH/BAQDAgEGMA8GA1Ud
EwEB/wQFMAMBAf8wHQYDVR0OBBYEFDwmUQSPaWT0KLIsusngsAqLEzazMB8GA1Ud
IwQYMBaAFDwmUQSPaWT0KLIsusngsAqLEzazMA0GCSqGSIb3DQEBCwUAA4ICAQBj
cRRrbaHJ/iyW88vu1DqVXJLl5XlT/OF3ht5JcC+ZkDdhr4QlXJMfr8pvul8OvJH9
O2UDMQYcQq0pAb87uCdc855HXSCP04eo8g3Bo6KGdxzxRr08yxjb0j3tB9LekN00
5bUKm4Ka9Trqm83f0UP5GAnNMkj1BlvzR2ucHutk1K9U2YLypy6FEr6bKZYSnnUh
dUJvyTiDd238k1glZytGmpRCzWjyWYi6p8VKmem9OhupXiPuxGrijrugtcLCPktk
1UQOPm/EYzPkf94ojC0vqRVHhSBZ1BSL9vzxBXEEGwr1F0+OjanPpUJnGbo2bzMh
R0XmJ7a4yzJUgfu+sX1p2ScnTCMHqjR8aCCM4aEgfZXg21Lhngb0n1L/YdeRi/g7
MnMIM5vDItKS1sSUAK1tK9AcqS9KYh48s0oqsc7cFA4d/EDGNYHZNs2HtaxohUQ0
LiPewUIXDc7hviCo5c4DfUNPmwxLdTYI4HyMM9gs0wC8KnW80V9Tnw3nXIRy5CTo
4XZx0mmuUYX31g0OzyO6PspcXTLx8XMvrUWXAQZ+h/0S7Egp4h9qXaiHp1ltJq0n
/t785BTBB4+HGIBZlzEIPdXpKS8eezaq6YCU2wAf67u4qYaOycKl6JStkmCyKIrF
/F3VcAAelOZ9UBxLSbVHmDLUN1+SDBWFPKw9q73vrQ==
-----END CERTIFICATE-----`
)

var (
	inst           istio.Instance
	fleetProjectID string
	revision       string
	caPool         string
)

// TestCAMigration: test zero downtime migration from MeshCA CA to Private CA
func TestCAMigration(t *testing.T) {
	// nolint: staticcheck
	framework.NewTest(t).
		Run(func(ctx framework.TestContext) {
			nsA := namespace.NewOrFail(ctx, namespace.Config{
				Prefix: "nsa",
				Inject: true,
			})

			nsB := namespace.NewOrFail(ctx, namespace.Config{
				Prefix: "nsb",
				Inject: true,
			})

			builder := deployment.New(ctx)

			echos, err := builder.WithClusters(ctx.Clusters()...).
				WithConfig(addNamespaceToConfig(util.EchoConfig(ASvc, false, nil), nsA)).
				WithConfig(addNamespaceToConfig(util.EchoConfig(BSvc, false, nil), nsB)).
				Build()
			if err != nil {
				t.Fatalf("failed to bring up apps for ca_migration: %v", err)
				return
			}
			cluster := ctx.Clusters().Default()
			a := match.And(match.ServiceName(echo.NamespacedName{Name: ASvc, Namespace: nsA}), match.Cluster(cluster)).GetMatches(echos)
			b := match.And(match.ServiceName(echo.NamespacedName{Name: BSvc, Namespace: nsB}), match.Cluster(cluster)).GetMatches(echos)

			checkConnectivity(t, ctx, a, b, "init-same-ca-mtls")

			workingDirs := setupCAMigration(t, ctx, "mesh_ca", "", "gcp_cas", caPool)

			migrateCA(t, ctx, workingDirs, fmt.Sprintf("%s,%s", nsA.Name(), nsB.Name()), []string{
				privateCATrustAnchor,
				meshCATrustAnchor,
			})
			if err := b[0].Restart(); err != nil {
				t.Fatalf("revisioned instance rollout failed with: %v", err)
			}

			if err := verifyCA(t, ctx, workingDirs, nsB.Name(), privateCATrustAnchor); err != nil {
				t.Fatalf("unable to verify nsB workloads signed by privateca: %v", err)
			}
			checkConnectivity(t, ctx, a, b, "cross-ca-mtls")

			if err := a[0].Restart(); err != nil {
				t.Fatalf("revisioned instance rollout failed with: %v", err)
			}

			if err := verifyCA(t, ctx, workingDirs, nsA.Name(), privateCATrustAnchor); err != nil {
				t.Fatalf("unable to verify nsA workloads signed by privateca: %v", err)
			}

			checkConnectivity(t, ctx, a, b, "post-migration-same-ca-mtls")

			rollbackCA(t, ctx, workingDirs)
			if err := a[0].Restart(); err != nil {
				t.Fatalf("revisioned instance rollout failed with: %v", err)
			}
			if err := b[0].Restart(); err != nil {
				t.Fatalf("revisioned instance rollout failed with: %v", err)
			}

			if err := verifyCA(t, ctx, workingDirs, fmt.Sprintf("%s,%s", nsA.Name(), nsB.Name()), meshCATrustAnchor); err != nil {
				t.Fatalf("unable to verify all workloads signed by meshca %v", err)
			}
			checkConnectivity(t, ctx, a, b, "rollback-same-ca-mtls")
		})
}

func TestMain(t *testing.M) {
	// Integration test for testing CA migration
	// Tests migration of workloads between CA's in the same control plane
	framework.NewSuite(t).
		Label(label.CustomSetup).
		Setup(istio.Setup(&inst, setupEnv)).
		Run()
}

func setupEnv(_ resource.Context, cfg *istio.Config) {
	var ok bool
	caPool, ok = os.LookupEnv("CA_POOL")
	if !ok {
		caPool = defaultCAPool
	}
	fleetProjectID, ok = os.LookupEnv("GCR_PROJECT_ID_1")
	if !ok {
		fleetProjectID = ""
	}
	// ASM e2e do not use revision labels by default
	revision = "default"
}
