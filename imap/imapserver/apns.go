package imapserver

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"time"

	"spilled.ink/imap/imapparser"
)

// APNS sends message notifications to the Apple Push Notification Service.
//
// To send notifications you need a certificate from Apple.
// It can be generated as a .p12 file from the old Mac Server App.
// Then it can be converted to cert/key files with:
//
//	openssl pkcs12 -in apns.mail.p12 -out apns.crt.pem -clcerts -nokeys
//	openssl pkcs12 -in apns.mail.p12 -out apns.key.pem -nocerts -nodes
type APNS struct {
	Certificate tls.Certificate // create with tls.LoadX509KeyPair
	GatewayAddr string          // default value: gateway.push.apple.com
	UID         string          // default value extracted from Certificate

	ctx              context.Context
	ctxCancel        func()
	shutdownComplete chan struct{}
	notify           chan imapparser.ApplePushDevice
}

// http://www.alvestrand.no/objectid/0.9.2342.19200300.100.1.1.html
var oidUserID = []int{0, 9, 2342, 19200300, 100, 1, 1}

func (a *APNS) start() error {
	if a.GatewayAddr == "" {
		a.GatewayAddr = "gateway.push.apple.com:2195"
	}
	if a.UID == "" {
		leafCert, err := x509.ParseCertificate(a.Certificate.Certificate[0])
		if err != nil {
			panic(err)
		}

		for _, n := range leafCert.Subject.Names {
			if n.Type.Equal(oidUserID) {
				if v, ok := n.Value.(string); ok {
					a.UID = v
					break
				}
			}
		}
		if a.UID == "" {
			return errors.New("APNS: certificate has no UID")
		}
	}

	a.ctx, a.ctxCancel = context.WithCancel(context.Background())
	a.shutdownComplete = make(chan struct{})
	a.notify = make(chan imapparser.ApplePushDevice, 32)
	go a.sender()
	return nil
}

func (a *APNS) shutdown() {
	a.ctxCancel()
	<-a.shutdownComplete
}

func (a *APNS) Notify(devices []imapparser.ApplePushDevice) {
	for _, device := range devices {
		select {
		case a.notify <- device:
		case <-a.ctx.Done():
		}
	}
}

func (a *APNS) sender() {
	for {
		select {
		case <-a.ctx.Done():
			close(a.shutdownComplete)
			return
		case device := <-a.notify:
			a.send(device)
		}
	}
}

func (a *APNS) send(device imapparser.ApplePushDevice) {
	config := &tls.Config{}
	if a.Certificate.Certificate != nil {
		config.Certificates = []tls.Certificate{a.Certificate}
	}
	c, err := tls.Dial("tcp", a.GatewayAddr, config)
	if err != nil {
		log.Printf("APNS: %v", err) // TODO better logging
		return
	}
	defer c.Close()

	buf := new(bytes.Buffer)
	for {
		buf.Reset()
		buf.WriteByte(0)
		buf.WriteByte(0)
		buf.WriteByte(0x20)

		token, err := hex.DecodeString(device.DeviceToken)
		if err != nil {
			log.Printf("APNS: bad token: %v: %v", device, err)
			continue
		}
		buf.Write(token)
		buf.WriteByte(0)

		data := map[string]interface{}{
			"aps": map[string]interface{}{
				"account-id": device.AccountID,
			},
		}
		jsonText, err := json.Marshal(data)
		if err != nil {
			panic("APNS: bad JSON: " + err.Error())
		}
		if len(jsonText) > 1<<8-1 {
			log.Printf("APNS: JSON too big: %d", len(jsonText))
			continue
		}
		buf.WriteByte(byte(len(jsonText)))
		buf.Write(jsonText)

		if _, err := buf.WriteTo(c); err != nil {
			log.Printf("APNS: failed to write: %v", err)
			// Slow down. Don't overwhelm the gateway on error.
			time.Sleep(1 * time.Second)
			return
		}
		log.Printf("APNS push notification sent for %v", device)

		select {
		case device = <-a.notify:
			// loop with new device
		case <-a.ctx.Done():
			return
		case <-time.After(5 * time.Second):
			return
		}
	}
}
