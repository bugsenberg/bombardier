package main

import (
	"crypto/tls"
	"encoding/pem"
	"io/ioutil"
	"log"
	"strings"
)

var (
	ioutilReadFile = ioutil.ReadFile
	logFatalf      = log.Fatalf
	tlsX509KeyPair = tls.X509KeyPair
)

// readClientCert - helper function to read client certificate
// from pem formatted file
func readClientCert(filename string) []tls.Certificate {
	if filename == "" {
		return nil
	}
	var (
		pkeyPem []byte
		certPem []byte
	)

	// read client certificate file (must include client private key and certificate)
	certFileBytes, err := ioutilReadFile(filename)
	if err != nil {
		logFatalf("failed to read client certificate file: %v", err)
	}

	for {
		block, rest := pem.Decode(certFileBytes)
		if block == nil {
			break
		}
		certFileBytes = rest

		if strings.HasSuffix(block.Type, "PRIVATE KEY") {
			pkeyPem = pem.EncodeToMemory(block)
		}
		if strings.HasSuffix(block.Type, "CERTIFICATE") {
			certPem = pem.EncodeToMemory(block)
		}
	}

	cert, err := tlsX509KeyPair(certPem, pkeyPem)
	if err != nil {
		logFatalf("unable to load client cert and key pair: %v", err)
	}
	return []tls.Certificate{cert}
}

// generateTLSConfig - helper function to generate a tls configuration based on
// config
func generateTLSConfig(c config) *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: c.insecure,
		Certificates:       readClientCert(c.clientCert),
	}
}
