// Package tlstest provides a pair of TLS configurations for testing.
//
// It can be used to establish TLS connections in tests:
//
//	srv := &http.Server{
//		TLSConfig: tlstest.ServerConfig,
//	}
//	// ...call srv.ServeTLS
//
//	client := &http.Client{
//		Transport: &http.Transport{TLSClientConfig: tlstest.ClientConfig},
//	}
//	resp, err := client.Get("https://localaddr-of-srv/")
//	// ...
//
// Note that if you are exclusively testing with HTTP, the standard
// library provides this in net/http/httptest.
//
// This package is intended for more general-purpose TLS socket testing.
package tlstest

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"

	_ "testing"
)

var ClientConfig = initClientConfig()
var ServerConfig = &tls.Config{
	Certificates: []tls.Certificate{cert},
}

func initClientConfig() *tls.Config {
	certpool := x509.NewCertPool()
	certificate, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		panic(fmt.Sprintf("tlstest: %v", err))
	}
	certpool.AddCert(certificate)
	return &tls.Config{
		RootCAs: certpool,
	}
}

var cert = initCert()

func initCert() tls.Certificate {
	cert, err := tls.X509KeyPair([]byte(testCert), []byte(testKey))
	if err != nil {
		panic(fmt.Sprintf("tlstest: %v", err))
	}
	return cert
}

// Generated using GOROOT/src/crypto/tls:
//	go run generate_cert.go -rsa-bits 2048 --host 127.0.0.1,::1,::,example.com,localhost \
//		--ca --start-date "Jan 1 00:00:00 1970" --duration=1000000h
const testCert = `-----BEGIN CERTIFICATE-----
MIIDNzCCAh+gAwIBAgIQL4Vm/jbjVDL1W/+QmE6GizANBgkqhkiG9w0BAQsFADAS
MRAwDgYDVQQKEwdBY21lIENvMCAXDTcwMDEwMTAwMDAwMFoYDzIwODQwMTI5MTYw
MDAwWjASMRAwDgYDVQQKEwdBY21lIENvMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8A
MIIBCgKCAQEA60v8OepbfpjeHtmw48R8yPdW4XXyWQGWCfwIQ0UqdVZX9cT9L1Dx
2Em3Pu11LWhfIDApgqVFHy7PdIY+fhKNPMui7Qh/y7OSIO71wWcL0G5yoW8exiGa
/w61sZFa56KPxhC09k0pX86a6VOufxKs79foVlTPM+iCBRvsryYodUJjdsY9WZlO
VBZvDEVOkcf58CwgkBYO8WbaVxK6tuvL66pOrUaKSZUzFAE9zpIwavKucNaYTod7
HCSjBHhJ+YqRvudFNQWyLF2jQYHFaUN4DpjFJwy/8vZ8XdaOoxQKMCJWbtO7+GG8
0mrkXUKxfnAMZDGo0WoGWt7xPsYwPmwQ0QIDAQABo4GGMIGDMA4GA1UdDwEB/wQE
AwICpDATBgNVHSUEDDAKBggrBgEFBQcDATAPBgNVHRMBAf8EBTADAQH/MEsGA1Ud
EQREMEKCC2V4YW1wbGUuY29tgglsb2NhbGhvc3SHBH8AAAGHEAAAAAAAAAAAAAAA
AAAAAAGHEAAAAAAAAAAAAAAAAAAAAAAwDQYJKoZIhvcNAQELBQADggEBAMZiLweQ
t4BQ0t56paXh/o5FEdHdEtK+JT9JtBSI6ZHLrNj0riGshPdLJYgLbU4g8mhzZ/Ob
snSXCf2sJqSVLNfMneFoLEXp9e5xeOGQMcbuV84NlbYb7reFZk/Eex1pnCUtlPHH
AB+c1Y/QQFlj1qbUI4P03O7pAGh979WdYOOp9/XpO52VI9pMCaYOEnkEUNjvm4Ja
BjCZBDrQYCBZHRQLQ7+EjvRfWLPqBjf9Z6U8R3ey9+1CX5k4zo3Z7hhoEyPnncZ9
duelzBygFffq9za6iKW+aIkkNtDLr64H5yLtoDZdc2MMXRzMEv2qtyM4/VLmtQ3r
DW5w0S9gkD6oxaM=
-----END CERTIFICATE-----`

const testKey = `-----BEGIN RSA PRIVATE KEY-----
MIIEpQIBAAKCAQEA60v8OepbfpjeHtmw48R8yPdW4XXyWQGWCfwIQ0UqdVZX9cT9
L1Dx2Em3Pu11LWhfIDApgqVFHy7PdIY+fhKNPMui7Qh/y7OSIO71wWcL0G5yoW8e
xiGa/w61sZFa56KPxhC09k0pX86a6VOufxKs79foVlTPM+iCBRvsryYodUJjdsY9
WZlOVBZvDEVOkcf58CwgkBYO8WbaVxK6tuvL66pOrUaKSZUzFAE9zpIwavKucNaY
Tod7HCSjBHhJ+YqRvudFNQWyLF2jQYHFaUN4DpjFJwy/8vZ8XdaOoxQKMCJWbtO7
+GG80mrkXUKxfnAMZDGo0WoGWt7xPsYwPmwQ0QIDAQABAoIBAHNgpyWfDY5eV0y5
YkvNpYLGBgw4UcXjSTdMJqEV4WP4Gtmg5qW1A2ITg4+P0M2bSEn4U+KEOAi6Y2+4
BBy97BPLpvCkIkY4n4cWpdtYNCrYfc07N9Pf1qkLBX000WaUB/wPZS0BWTBplvyi
1AXrmnFhZcQvggrqEBeBQeYAyAX2vxhPPy0pHoUmGTJERm6J8zpK1HqKQpzE9foX
xEGgVCH3Zgo3ZsBlIHCVF/VuTnoMhhwlS2JBdr57npv2fw+HsfY/ophYerJokH7r
hUUhzNO4wPkdOZkKgIx53jAWLDl7ZSN8rUo/X0ix/UEMgr+1iM5hOlXFEgvQuH1J
+xmRESECgYEA+9Y1DXRSu7KOLLbAlslvLgLwIYRqHukjlFv4s0927oGgOrfLPNEi
PSp92pphqEYFqqrkDzuerKIRE/d6BvDGbOvrK/7BEL+GArnrSN2T7A7rGubw2AIb
t41Y3RETz6HxAk9GiEbBb/hCD4qGDW0wqYDphToh3Kys7Cd2N/aGdxcCgYEA7y/H
napziUbPE0yNgcgWwlViHbhjPs8qNLfCCDi03efgVdeMoRWzo5ekE4W2hekwDnlx
/1vXNZdPKzDEsLpTVNQegWjH42zxmv1Dek9XdSPEspWFOowxjiSYHrkIOWk7mZjQ
TXLxepvzH76Vm7+8WuP3c8Ur7qkC48bNg1+SKFcCgYEAkennC0ietwoZvmaU58kG
lg41u/XQ1uAWMVuomZwtOLv6bosXQsGZqP75tLNGag1IMz6YrQrKQRQV+Q+msGbJ
UUrQE8mja2TM7L90R9+6WUe7iPbODRoLnSpUlqHSbLdTwRbVsxfr9EhPXlnQme7u
BwgeRYcNH6Mc/idPI9W+yzkCgYEAgQ0mhssQy2CJGcCUGRH8NZ4b8i0qXxknjIoZ
BpaR/6i8QZSrK76pzfpjbKUYdef7JdQgzcaftyqMbKFDfpcJnxtT2j7OmsaNFTLQ
1Y05gtpppnFGEPDTS/4ylWEALvm4TodE3ITIBX9fDiGmVwJ8fg3B1ZTsvzgxdvQs
rlVCZsECgYEArpTuVyIAahDFRsaTnG0mEOkINBvBUmu+9+U5E6kGVQOyR/UgAYtK
YUCZ7E7fCBBBeWzJDtaL0PLn/78HJwOidJxqa0HCI3sTNXnyin+qDKg7RdvLDYwx
R4W4owFagG6iKO/I3q0ZZ1sm+DV4XGzDv166CeSFdi2vvprT8F145gw=
-----END RSA PRIVATE KEY-----`
