package main

import (
	"bytes"
	"context"
	"crypto/tls"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
)

var l = log.Println

var configMap map[string]any

//go:embed config.json
var configFile []byte

type SockRead struct {
	n   int
	err any
}

func handleClient(client net.Conn, client1 *http.Client) {

	sbreq := make([]byte, 8) //this is to much even, literally all of this request packets
	n, err := client.Read(sbreq)
	if err != nil {
		return
	}
	if n < 3 || sbreq[0] != 0x05 {
		log.Println("Invalid request to proxy from", client.RemoteAddr().String())
		return
	}
	log.Println("First request!")
	sbresp := make([]byte, 2)
	sbresp[0] = 0x05
	sbresp[1] = 0x00
	client.Write(sbresp)
	log.Println("Sub negotiation finished for", client.RemoteAddr().String())
	areq := make([]byte, 8192*2) //16kb
	_, err = client.Read(areq)
	if err != nil {
		return
	}
	if areq[0] != 0x05 && areq[2] != 0x00 {
		log.Println("Invalid request to proxy after sub negotiation")
		return
	}
	var dstaddr string
	var dstport uint16
	var portoffset uint8
	switch areq[3] { // ATYP
	case 0x01: // IPv4
		strbuf := make([]byte, 4)
		strbuf[0] = areq[4]
		strbuf[1] = areq[5]
		strbuf[2] = areq[6]
		strbuf[3] = areq[7]
		dstaddr = string(strbuf)
		portoffset = 8
	case 0x03: // DOMAINNAME
		log.Println("DOMAIN NAME!")
		numberOfBytes := areq[4]
		log.Println("number of octets", numberOfBytes)
		log.Println("the damn thing", string(areq[5:numberOfBytes+5]))
		dstaddr = string(areq[5 : numberOfBytes+5])
		portoffset = 5 + numberOfBytes
	case 0x04: // IPv6
		for i := 4; i < 20; i += 2 {
			hexed := fmt.Sprintf("%X", binary.BigEndian.Uint16(areq[i:i+2]))
			dstaddr += hexed + "."
		}
		runes := []rune(dstaddr)
		dstaddr = string(runes[:len(runes)-1])
		portoffset = uint8(len(runes)) - 1
	}
	dstport = binary.BigEndian.Uint16(areq[portoffset : portoffset+2])
	log.Println("FUCKING PORT", dstport)

	switch areq[1] { // CMD
	case 0x01: // CONNECT
		log.Println("CONNECT")
		response := make([]byte, 10)
		response[0] = 0x05 // VER
		response[1] = 0x00 // REP
		response[2] = 0x00 // RSV
		response[3] = 0x01 // ATYP
		// many clients dont give a fuck about server bound ip/port
		response[4], response[5], response[6], response[7], response[8], response[9] = 0, 0, 0, 0, 0, 0
		client.Write(response)
		log.Println("we wrote the reply")
		log.Println("the dstaddr", dstaddr)
		dst := net.JoinHostPort(dstaddr, fmt.Sprintf("%d", dstport))
		log.Println("the address", dst)
		defer client.Close()

		buffer := make([]byte, 16384) // 16kb
		_ = buffer
		id := uuid.New().String()
		l("fucking id", id)
		// write goroutine

		var wg sync.WaitGroup

		wg.Go(func() {
			for {
				n1, error := client.Read(buffer[:])
				if error != nil {
					if error == io.EOF {
						l("End of file")
						myJson := map[string]any{
							"id":   id,
							"type": "close",
						}
						jsonData, _ := json.Marshal(myJson)
						requestBody, err := http.NewRequest("POST", configMap["appscript_url"].(string), bytes.NewBuffer(jsonData))
						requestBody.Header.Set("Content-Type", "application/json")
						requestBody.Host = "script.google.com"
						resp, error1 := client1.Do(requestBody)
						if error1 != nil {
							l("fucking problem with POSTing an asshole", err)
						}
						resp.Body.Close()
					}
					wg.Done()
					break
				}
				request := base64.StdEncoding.EncodeToString(buffer[:n1])
				var myJson = map[string]any{
					"id":      id,
					"dstaddr": dstaddr,
					"dstport": dstport,
					"data":    request,
					"type":    "first",
				}
				jsonData, _ := json.Marshal(myJson)

				requestBody, err := http.NewRequest("POST", configMap["appscript_url"].(string), bytes.NewBuffer(jsonData))
				requestBody.Header.Set("Content-Type", "application/json")
				requestBody.Host = "script.google.com"
				resp, error1 := client1.Do(requestBody)
				if resp == nil {
					break
				}
				l("ass response of POST:", resp)
				l("body response of POST:", resp.Body)
				if error1 != nil {
					l("fucking problem with POSTing an asshole", err)
					break
				}
				resp.Body.Close()
			}
		})

		wg.Go(func() {
			for {
				var myJson = map[string]any{
					"id":   id,
					"type": "next",
				}
				var jsoned map[string]any
				jsonData, _ := json.Marshal(myJson)

				requestBody, err := http.NewRequest("POST", configMap["appscript_url"].(string), bytes.NewBuffer(jsonData))
				requestBody.Header.Set("Content-Type", "application/json")
				requestBody.Host = "script.google.com"
				resp, error1 := client1.Do(requestBody)
				if resp == nil {
					break
				}
				l("dick response of POST:", resp)
				l("body response of POST:", resp.Body)
				bytesa, _ := io.ReadAll(resp.Body)
				l("stringfied body response of POST:", string(bytesa))
				if error1 != nil {
					l("fucking problem with POSTing an asshole", err)
					break
				}

				location := resp.Header.Get("location")
				somepart := location[36:]

				secondReq, err := http.NewRequest("GET", "https://www.google.com"+somepart, nil)

				secondReq.Host = "script.googleusercontent.com"
				resp, error1 = client1.Do(secondReq)
				l("response of GET:", resp)
				l("response body of GET:", resp.Body)
				if error1 != nil {
					l("fucking problem with GETing an asshole", err)
					break
				}
				bytesa, _ = io.ReadAll(resp.Body)
				l("stringifed response body of GET:", string(bytesa))
				if string(bytesa) != "null" {
					l("this isn't null which means we can encode it to json maybe")
					json.Unmarshal(bytesa, &jsoned)
				} else {
					l("response chunk is null whiich means we should retry or maybe?") // i dont i should break or continue?
					continue
				}
				l("we passed the whole shit now we have the chunk of response")
				packet, _ := base64.StdEncoding.DecodeString(jsoned["data"].(string))
				l("id:", id, ",seq:", jsoned["seq"])
				client.Write(packet)
				resp.Body.Close()
			}
		})

		wg.Wait()
	}

}

func main() {
	json.Unmarshal(configFile, &configMap)
	dialer := &net.Dialer{
		Timeout: 30 * time.Second,
	}
	myTransport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			addr = "216.239.38.120:443"
			return dialer.DialContext(ctx, network, addr)
		},
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: "www.google.com",
		},
	}
	client1 := &http.Client{
		Timeout:   30 * time.Second,
		Transport: myTransport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	log.Println("Listening on 12345")
	listener, error := net.Listen("tcp", "0.0.0.0:12345")
	if error != nil {
		log.Println(`Port is in use maybe`, error)
	}
	go func() {
		signalc, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
		defer stop()
		<-signalc.Done()
		l("Sending fullclose signal to receiver with google sni...")
		myJson := map[string]any{
			"type": "fullclose",
		}
		jsonData, _ := json.Marshal(myJson)
		requestBody, err := http.NewRequest("POST", configMap["appscript_url"].(string), bytes.NewBuffer(jsonData))
		requestBody.Header.Set("Content-Type", "application/json")
		requestBody.Host = "script.google.com"
		_, error1 := client1.Do(requestBody)
		if error1 != nil {
			l("fucking problem with POSTing an asshole", err)
		}
		l("Done")
		syscall.Exit(0)
	}()

	for {
		client, _ := listener.Accept()
		go func() {
			handleClient(client, client1)
		}()
	}
}
