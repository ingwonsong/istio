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
	defaultCAPool     = "project/istio-prow-build/locations/us-central1/caPools/asm-testci-sub-pool-us-central"
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
	// in the istio-prow-build gcp project. need to retrieve this as env variable?
	privateCATrustAnchor = `-----BEGIN CERTIFICATE-----
MIIFWjCCA0KgAwIBAgITBw/2mWqrIf5LelhYhvRTDQnWQTANBgkqhkiG9w0BAQsF
ADA1MRQwEgYDVQQKEwtBU00tVEVTVC1DSTEdMBsGA1UEAxMUYXNtLXRlc3RjaS1y
b290LWNhLTIwHhcNMjIwMTE0MjIxNzEzWhcNMzIwMTE1MDgyNDUzWjA1MRQwEgYD
VQQKEwtBU00tVEVTVC1DSTEdMBsGA1UEAxMUYXNtLXRlc3RjaS1yb290LWNhLTIw
ggIiMA0GCSqGSIb3DQEBAQUAA4ICDwAwggIKAoICAQCwnajtBksl4pTnip1QcDdU
yz9iPGpfA0uln9e8NXdEx0CY9D5+NP+zEOHY6VQPsUVlqZxb319/q1b0BflSeZmT
qZGF7x/29AgA6H6L102azfxNRQsl0YCPZYwaE+1bp1Nsa2T9oBJnBXtGd+gRAAbE
mluR9ta8cgHeZh2kASvDx6OQX0qQMU+I9Xr6fqiYgE5TejWH4mWq9TtFLF82kUsg
bRtv09TWskpD/VMnUguC51q2gmcpyKimlb35jI18rDUOPz446ZHk0PJDvMlOJWQ8
7CFwpTtcNaGE+O30upIsJq9ioQTtowxXKWCXYQao5nF4RlAkIaGbZDqICbR/kxIn
ua7u1XLsVkNZYEZzSllCeVuLavHBuEtBjPE6iDja+CuVRjI1Np1yl5oyE76wYBfQ
2kbVDRzQObWKBaTIqeYWWSdsF4q6l5UtW1bctG9jba2C1um+S3oE3iKAK7ONESc8
TZ7fSwqdA06ZLjKLbD3MCcn42ar05biH6tB+bJjQxMGviI3QtDUhOCGDCWGTMaaD
0INia/yWmlkF8Gb5NQF1Ky0MWPQwu+B27p2wtkWv8jONFIGzjL1t6A4oKy4WkLNB
m6FOaVX9EzH1ea4OEAq7MkdokoLfzv1kT0CeOoMBwUZulKifr338MUfQZtnjxLkU
3qxtx2om/XjFa+1hv2p3VwIDAQABo2MwYTAOBgNVHQ8BAf8EBAMCAQYwDwYDVR0T
AQH/BAUwAwEB/zAdBgNVHQ4EFgQUsnKhZS/Id1KgJONt83RzY9yzPxQwHwYDVR0j
BBgwFoAUsnKhZS/Id1KgJONt83RzY9yzPxQwDQYJKoZIhvcNAQELBQADggIBAB0I
Xmc4ce48eDFW8qXkZ0q9Mkcnml8fU6h8lZ0+8iR0jjg+x3t2T2yGJmJEncM7XoZc
RVWc3J+sqBpuBOoZkh21+tHa3np+gz2xxjPdl+dIdaSZLv7xTSyn1HI/lhQY1Rap
idkrk4ZBGRQHhdBwI6czjMKfTbwRKnx+nFRucP4KXAkIwl5jKlxRrodTePYZF/Jx
5W6UsrnIn9fkffybzDLWhl7eTbpeFclK8B45+t7Qd0/EQFOdwUJToM3eQsYWUXXN
5o7CiZFQ2aocuggZEhId13jVKD1azvzU9oCLxLDWXQYEAjfsXxPCLMF3bmaKcLJP
C78sGVHMSqLqiy/OKYKkwpicsxBwvDcVAP43JXs4sj6lmoaOba3qyNK2lxclisP/
MV0a6jCv0hzQXwD+c7opicOsktX86axHhjImGCF1ON4j7uxEC1MhiTPNsCgXCqR4
XMjtlLOoJl3hApvmgC1OOLrt3P9EoT7wbDOG6j8xBCsJw/ftnVK6vhtbJPCCW4vn
gtbiutLrHnc8tYFIGvsPx3sMUgN7V8VEhNIXtrSMAyRWvZNdlTjbr8PXzhdqQ+DY
ICV9a4jdB0ZIqFKP26pxVQguNJizdINb4zc5GROItRHTEtLS55BpaCc9W/mKHp/Q
PRYXCIDCQtY1/kZ/otdh4ApGTDafZBvevO0L3D8A
-----END CERTIFICATE-----
-----BEGIN CERTIFICATE-----
MIIFWzCCA0OgAwIBAgIUAOuYQuxVsKWv3Ndisrb91mXZGg8wDQYJKoZIhvcNAQEL
BQAwNTEUMBIGA1UEChMLQVNNLVRFU1QtQ0kxHTAbBgNVBAMTFGFzbS10ZXN0Y2kt
cm9vdC1jYS0zMB4XDTIyMDExNDIyMTcyMVoXDTMyMDExNTA4MjUwMVowNTEUMBIG
A1UEChMLQVNNLVRFU1QtQ0kxHTAbBgNVBAMTFGFzbS10ZXN0Y2ktcm9vdC1jYS0z
MIICIjANBgkqhkiG9w0BAQEFAAOCAg8AMIICCgKCAgEApxui98wN8xTaeIVqUO7Q
xUQWogw+qPhsyXJzu+SvvB03FtSYNe+4vWzWiJhOfY3BC9mMfDjn3+TqkFdaPRul
0Si+VQ8I7h0+b0WIsYk4FpedAQhjJGfQtkJwu4ehs0QHcW+WnN2XNrWkogJcxh7d
gU188Q47pK3dQenl+J7qW1kEx2a4QG0gPqaFJ/dnOn85ed1NL5uvIKNySZ33BDww
HfpA9QUlFvqOtgXyQcv1v6vf75Yt5UoTkbFf4xeJphNEGaQgNySsdtSSbdTqbgjz
rzjHTSnV5iuKLPKp0W5gBNv5dzW/ym4Aruuh6hrRgpjOgL6kKctuM+SSg9qxb9nx
AfHOAn5JpnaDkfDq+Al0RpPTfD4J8U4k8tFqe8CwWV9yz/hisRnzSBGO/WmErh2M
gCDl8+SAhisnaY77qt2Vq7P8vrbCcDHQZJXNHmZyxkEarFULjKW20jccoil6/GkC
2eTzwgAO3Huzv/ZU8SIyQHjviZEmsqP/Pu0WDIK6Xo/o8/I5muFPfVlORoOcAI7o
05t4n2PVaEK7m7OeZbFfvjMwBNVTIf7tbMAXivGrhAMXcPpDaT8HygdV96QH/UrF
6ZhNPYNDvL2U2FYclCFnQrV1wDoId/yHiPgUZhvXnhQuYw1tpCTFi/zwnhBSA/uG
jNjkuv022Zrwz6i2hG+0Au8CAwEAAaNjMGEwDgYDVR0PAQH/BAQDAgEGMA8GA1Ud
EwEB/wQFMAMBAf8wHQYDVR0OBBYEFLmqINi9bBJk2E6haP09vsV0RbdaMB8GA1Ud
IwQYMBaAFLmqINi9bBJk2E6haP09vsV0RbdaMA0GCSqGSIb3DQEBCwUAA4ICAQCa
AVhcI8bPakR/H13LRROpBwDOT0rPZPDy6fQ1NoqE+B74KB1/f28w/7RHnYtBz524
zcUJycbEwZYammGMhwcXbrT4PEu3E7WDKSxEzn6pZAo2Jz+qjTZZ1Aq2bF1gK+jJ
0dJRzNmRVwmKNiLkoGzKxxaz+jRrpu33aUnNHwebY4Eq4KOz+7WbwAhLvmQZt61g
JQKBkg+7R5StE43n8+FPvH1OWY1vG8p0W27uVvsHwiBpObuNASSacXOSXU3OnE7d
g4/VsMex6nzK57pJDXbviHIy+J2IjkYKRUSyJufm4Ra1KeLuStb39jSYtemiP9ez
i+dI0luaAWKz1JZzi3vQuzUEVOYbiFYU6KP7+bjSuDMDWJrPvU4IN11219u9WooO
U6CxYtMLYJwi3GbGwye42tntkHb6VM3jz1zwpb0XBNK6mbbL6fif5wvZOf6e8v9U
Mf4PIni5y0fL+GtfxPi1JWL03FTOqwvjdLjgMM1D5HPhrvyX2adyqrc2WQrMUCpZ
tIBOKNtjW9I3Ie2Pcr6Ubmjob2xaJ6knrkmZxTflQm2mcuKniWFwTZ9YFN+ZZmnh
1oyXDKlFQ2dL5UpYhESM5fwrZgo6L6hgbK3OKw6CWrHX7H0FRTFOJMRxooaU19om
A+w6r2x4HTei5WEo9OoZz+euH28SF803u6Bh9RKFJQ==
-----END CERTIFICATE-----
-----BEGIN CERTIFICATE-----
MIIFWjCCA0KgAwIBAgITXqg5NqRCqJ2+0tNDKUBAJ6toXzANBgkqhkiG9w0BAQsF
ADA1MRQwEgYDVQQKEwtBU00tVEVTVC1DSTEdMBsGA1UEAxMUYXNtLXRlc3RjaS1y
b290LWNhLTEwHhcNMjIwMTE0MjIxNzA0WhcNMzIwMTE1MDgyNDQ0WjA1MRQwEgYD
VQQKEwtBU00tVEVTVC1DSTEdMBsGA1UEAxMUYXNtLXRlc3RjaS1yb290LWNhLTEw
ggIiMA0GCSqGSIb3DQEBAQUAA4ICDwAwggIKAoICAQCMCVYk7+KfNlMzqb5aymZm
RspDJW2PPb4whOt/oA3PgiQnZTPvECKMH/3gi20g0vEr9UH1cBmQpRz7AdaxbURf
xsBzo/kvHfE64LhM0+1YXAJNZOWCviQ4AsBT89TrIGtNUssX6SJwposLq8UyIZjP
j7zdajnQC7As55cogdI0ycEfm2kDlEaKcayTyO7lJ8F027urwhflN5ho7wy+7+Vy
fDEYOX03cpPQS+Ml3hQZ5833pNfFNpMivxkNBdMoVdPBYmvD0D8eWy2tEvanSBXT
cO7J7F1+XXwkolgqqBtKo6OQ4Ng6+eyhCr5o0leyu0COrHr7R/czOagVUnxZHaMb
Ewq2qyAcDa2bQunlQHwbGYNVFxOlYslcaH2aBsqN5YYHn6sEJhuFJ7AWsb4T73h+
HPPqcBou3TF03KsrJWwk0BDmcGFaX5IxxweJCLuGfnCrd+V0pnbITcNG3J6IL98j
RutlRrcJwLs+FzPhfK6sDJuGZ6hSHA03R0/l+GX6Bbk1AWcgxJDMYn+AgAlvXJKK
5b99mFagt/35TpSCnRifyXV2P+oJR7UEEFKnr4PxvDWi/OAn6OA9dGuw6O1F/8yu
0MqCQ+4nXW2y0jT9rseTxow/v/2gRtuis+HyvdONSHa5SH7MiuVTk10zi23sSrl1
tWUpjm6aNATk02udqC7aWwIDAQABo2MwYTAOBgNVHQ8BAf8EBAMCAQYwDwYDVR0T
AQH/BAUwAwEB/zAdBgNVHQ4EFgQUZZ14At0ZtdBRJf5ziB7AnLXCc+IwHwYDVR0j
BBgwFoAUZZ14At0ZtdBRJf5ziB7AnLXCc+IwDQYJKoZIhvcNAQELBQADggIBABfo
D2Ye3VNkD5htb3cKbPOf5UBvAWWkuNxM+lFtAn9SRo2pP3y+3nBdN7qUHeWHJT17
r5tbvMYqVz9kXyXQOcTFE9E4/zhMuh6TYKuMAfbSHgg1AA0lM9D8+SQiDsgo5YHi
eGqOFRcNDgxfxZU+0+CF69TJ+K0QtiWHnju0wNFNaGHkoG3UZPPsBAEVtkZV4iLT
O3+NgaCyGoB2KxBMfJ8OsvyviP4tXywrZcffVI73BDqtjir4jl9Q46MxGNeD2geA
dizTMh/x0p95Qzf0OTRgS4mfapwd7xyNARjMTYaEf/BA6hIeh9UkaEyRonzRl26w
H4OJRXMvqHJWGmM0SbGcedVBmYkKg0brTV5EKqc7lE0XlbYc24xWqs3kDp3rq2OY
HglhkSSoiAOTaqu9h+v8/+hLX4NVT1dSSQFaqYRg9QHbRL7cz0TNIhHwJraxUoK7
foZAB6G9bfhqjwt/r+Cghp10CK69TxtSls82doinWgwCJvcalmfBgiH+kqTObrj8
pSn6UbMFtzNHhPRIOtz6sA2t+BS26J2KvjlMQcfu/MdLLRCyZdlo2DEXbsLL6X5P
7nDfgiS15FI+jFgMlKa41lu/avwU0y9I9ixYT4rz6wMEJ+xVi2W3AOjQSFz36SeL
ylz06NSsPBIRybRr722ciHXXaOLATKb6DwRUfUzL
-----END CERTIFICATE-----
-----BEGIN CERTIFICATE-----
MIIFWjCCA0KgAwIBAgITa/sh8pVpm/duUgk9qaZHYm/LszANBgkqhkiG9w0BAQsF
ADA1MRQwEgYDVQQKEwtBU00tVEVTVC1DSTEdMBsGA1UEAxMUYXNtLXRlc3RjaS1y
b290LWNhLTQwHhcNMjIwMTE0MjIxNzI5WhcNMzIwMTE1MDgyNTA5WjA1MRQwEgYD
VQQKEwtBU00tVEVTVC1DSTEdMBsGA1UEAxMUYXNtLXRlc3RjaS1yb290LWNhLTQw
ggIiMA0GCSqGSIb3DQEBAQUAA4ICDwAwggIKAoICAQCusUHqNHoza+37Mmak5lHW
2j2EmbmqyH80PWkpaZXdt0V33qOWurBTdwgQSMAzZ8DubXkI9AXKFt/HY4Pql7OP
Fx9XMOhAQ9fGpYm/vx5vQ0ExBMgP2HrR9Vgl/pn3kBaR3JpLvRU6PrgamEMoLxKd
yrJsW1yWmGs/YFzp4VbAw+orks2zVG8n9ItEsvGygbxDOoBRpXjdu5OWat4wrWNm
asl30YWlVk0upgdyWL79Y9aMpEnViYzGief52sDEzWfhIEeD3FXaqvhhZVqDiVCw
iukPFWwl6mxdPBVp0Xnb0225EKbAF4D6Z1w3ONBDu9+AItkMM+l73w+ER096thSq
D2G5Itdmvp93oShkHNi2bOiMzNlOefPtR3aGkR2HWUsUhr2jM9893u8ZuEN9FDKn
d/Pup2IaZVnO5JHPEk1UDGFzqfbKnRuKFD2eRG/eWa+FA46UpuJo8VYrXwkdMmdl
LaAIWmsx19L5Ea4rfpjV9a2sz234sbA32CKXSHCMkQ1Qfm6VcW2HuuwLvgFHGV+s
/x/W8nuwl3D/h3MJO6Xz4jPEYHfS3POI533LMAT1UH5MaLtVahMaAkZyTTOAr5+Z
Vp2PYWlY833pGgBn9ShuVDD9aWKrH1nO/AS/GjV6WkOxhn+qSlNPafYWlP1KANkE
OyGfcjsIUKP7oODYO3urTQIDAQABo2MwYTAOBgNVHQ8BAf8EBAMCAQYwDwYDVR0T
AQH/BAUwAwEB/zAdBgNVHQ4EFgQU/37nEJAKNTvkqQ5/aJH9dM6sAJQwHwYDVR0j
BBgwFoAU/37nEJAKNTvkqQ5/aJH9dM6sAJQwDQYJKoZIhvcNAQELBQADggIBACgO
/CVSkgUa4/Cuj1RvpQj2QzFZHWAlp76nWDhtKcyTVTDtr06pDuHrleYD8UTEFta3
MT6GEtBzQMQH2zzh/4MusYb3M+vthVHs/fRH34yy2z/EhgpwpbePUoU3PKTalH1h
4RlJ4XIinMaCyz0Idlid+K6SYDm1oxyHwTwnpIydD7IQmpS4rLV+7hSs7tufZoyh
XlESLK3JKuR4CB3aY9YNzPUGxgAh6pykQykGsJ2aU2y41mG4WN5ELx/ejtkGmYgd
C99uddu4bE2/7YleqY066p9xyIlfJmb2/yoosPFNvkRp5olCjVaFcQuuiXCLZk3v
1EFPCZZKEaUanVZJrUTjnNEvHTloaJLlOhNPkxxOK4Hg1XEgBrXKkEYNWmhQUlsh
F2uCukj6W9MHYJVQ5WncQV250O/AyzZ8QwNJkZRxwDSBqV4g+Snf7/hteiKf2BHU
IhtM0J5diF9IEyKzx0BVLwKH7tjnVnK4rsrc8gEOFO5dhNAz+kA255wqWMWdAZIC
qThnfpfNyfCviVm0GAH5a8WSFYBiSKXjQGgDki9He/Ub6CKlAsOoBf/Rx3hLWU1W
Lo/2dqF4fUdMppYFBJ2rcB1u35MDetnaKMSPK+zdUCQIRgc80cONc3VfYMb86zi3
KEGA5tyikRCbPgu76vVjS/6mvXEsHt7gqCG8cTJ5
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
		Features("security.migrationca.meshca-privateca").
		Run(func(ctx framework.TestContext) {
			nsA := namespace.NewOrFail(t, ctx, namespace.Config{
				Prefix: "nsa",
				Inject: true,
			})

			nsB := namespace.NewOrFail(t, ctx, namespace.Config{
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
