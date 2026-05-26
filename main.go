package main

import (
	"bufio"
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
	"time"

	"github.com/google/uuid"
)

var l = log.Println

var configMap map[string]any

//go:embed config.json
var configFile []byte

func handleClient(client net.Conn, transport *http.Transport) {

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
	n, _ = client.Write(sbresp)
	log.Println("Sub negotiation finished for", client.RemoteAddr().String())
	areq := make([]byte, 8192*2) //16kb
	n, err = client.Read(areq)
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
		connection, err := net.Dial("tcp", dst)
		if err != nil {
			log.Println("fucked")
			return
		}
		log.Print("here\n")
		defer connection.Close()
		defer client.Close()

		readIO := bufio.NewReader(client)
		buffer := [8192 * 2]byte{} // 16kb
		id, seq := uuid.New().String(), 0
		go func() {
			for {
				n, error1 := readIO.Read(buffer[:])
				if error1 == io.EOF {
					l("Problem with connecting to target server (SNI) or End of stream for", error1)
					break
				}
				requestChunk := base64.StdEncoding.EncodeToString(buffer[:n])
				var myJson = map[string]any{
					"id":      id,
					"seq":     seq,
					"data":    requestChunk,
					"dstaddr": dstaddr,
					"dstport": dstport,
				}
				seq++
				jsonData, _ := json.Marshal(myJson)
				req, err := http.NewRequest("POST", configMap["appscript_url"].(string), bytes.NewBuffer(jsonData))
				req.Header.Set("Content-Type", "application/json")
				req.Host = "script.google.com"
				client1 := &http.Client{
					Timeout:   time.Duration(configMap["sni_timeout"].(int)) * time.Second,
					Transport: transport,
					CheckRedirect: func(req *http.Request, via []*http.Request) error {
						return http.ErrUseLastResponse
					},
				}
				resp, error1 := client1.Do(req)
				if error1 != nil {
					l("fucking problem with POSTing an asshole", err)
					break
				}
				location := resp.Header.Get("location")
				somepart := location[36:]

				// second part
				secondReq, err := http.NewRequest("GET", "https://www.google.com"+somepart, nil) // we get response from here maybe?

				req.Header.Set("Content-Type", "application/json")
				secondReq.Host = "script.googleusercontent.com"
				resp, error1 = client1.Do(secondReq)
				if error1 != nil {
					l("fucking problem with GETing an asshole", err)
					break
				}
				bytes, _ := io.ReadAll(resp.Body)
				var validJMap map[string]any
				json.Unmarshal(bytes, &validJMap)
				l("FuckingBody ->", validJMap)
				packet, _ := base64.StdEncoding.DecodeString(validJMap["data"].(string))
				client.Write(packet)
			}
		}()
	}

}

func main() {
	json.Unmarshal(configFile, &configMap)
	dialer := &net.Dialer{
		Timeout: time.Duration(configMap["sni_timeout"].(int)) * time.Second,
	}
	myTransport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			addr = "216.239.38.120:443"
			return dialer.DialContext(ctx, network, addr)
		},
		TLSClientConfig: &tls.Config{
			ServerName: "www.google.com",
		},
	}

	log.Println("Listening on 12345")
	listener, error := net.Listen("tcp", "0.0.0.0:12345")
	if error != nil {
		log.Println(`Port is in use maybe`, error)
	}
	for {
		client, _ := listener.Accept()
		go func() {
			handleClient(client, myTransport)
		}()
	}
}
