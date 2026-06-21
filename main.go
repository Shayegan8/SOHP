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
	"os"
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

type SockDetail struct {
	socket  net.Conn
	dstaddr string
	dstport uint16
	chani   chan any
}

var sockets map[string]SockDetail = map[string]SockDetail{}

var socksmut *sync.Mutex = &sync.Mutex{}

type Request struct {
	id      string
	request string
}

var reqmut *sync.Mutex = &sync.Mutex{}

var requests = []Request{}

var shitMap = map[string]SockDetail{}

func handleClient(client net.Conn) {

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

		id := uuid.New().String()
		l("fucking id", id)

		now := time.Now()
		for {
			data := make([]byte, 4048)
			n, err := client.Read(data)
			if err != nil {
				l("data read error for", id, "err\n", err)
				break
			}
			data = data[:n]
			l("Request size is:", len(data))
			l("Encoded Request:", base64.StdEncoding.EncodeToString(data))
			socksmut.Lock()
			sockets[id] = SockDetail{client, dstaddr, dstport, make(chan any, 1)}
			socksmut.Unlock()
			if len(data) == 0 { // as i tested i never see err
				l("time took is:", time.Since(now))
				break
			}
			request := base64.StdEncoding.EncodeToString(data)
			l("request encoded")
			reqmut.Lock()
			requests = append(requests, Request{id, request})
			reqmut.Unlock()
			l("request pushed")
		}
		l("Total time, " + time.Since(now).String())
	}

}

func main() {
	json.Unmarshal(configFile, &configMap)

	args := os.Args
	if len(args) > 1 {
		switch args[1] {
		case "--help", "-h":
			l("StupidSNIUpstashProxy - By Shayegan8\n" +
				"--resolve, resolve - It resolves dnses of www.google.com destination and measures their speed\n" +
				"--help, -h - This command")
			return
		case "--resolve", "-r": // i'll use someone's else  list
			ips, errori := net.DefaultResolver.LookupHost(context.Background(), "www.google.com")
			if errori != nil {
				l("can't resolve google")
				return
			}
			l(ips)
			return
		}
	}
	dialer := &net.Dialer{
		Timeout: 360 * time.Second,
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
		Timeout:   360 * time.Second,
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

	ticker := time.NewTicker(500 * time.Millisecond)
	//requests/responses
	go func() {
		for {
			<-ticker.C
			reqmut.Lock()
			if len(requests) == 0 {
				reqmut.Unlock()
				continue
			}
			ids := map[string]map[string]any{}
			for _, value := range requests[:] {
				l("PRINT THE DAMN,", ids[value.id])
				if _, ok := ids[value.id]; !ok {
					socksmut.Lock()
					shitMap[value.id] = sockets[value.id]
					socksmut.Unlock()
					l("first jerk")
					ids[value.id] = map[string]any{
						"data":    []string{value.request},
						"dstaddr": shitMap[value.id].dstaddr,
						"dstport": shitMap[value.id].dstport,
					}
				} else {
					l("sec jerk")
					n := append(ids[value.id]["data"].([]string), value.request)
					ids[value.id] = map[string]any{
						"data":    n,
						"dstaddr": shitMap[value.id].dstaddr,
						"dstport": shitMap[value.id].dstport,
					}
				}
			}
			requests = nil
			reqmut.Unlock()
			l("I Unlock 1 req?")

			myJson := map[string]any{
				"ids":  ids,
				"type": "client_chunks",
			}
			l("RESULT:", ids)
			jsonData, _ := json.Marshal(myJson)

			requestBody, _ := http.NewRequest("POST", configMap["appscript_url"].(string), bytes.NewBuffer(jsonData))
			requestBody.Header.Set("Content-Type", "application/json")
			requestBody.Host = "script.google.com"
			l("Before resp")
			resp, error1 := client1.Do(requestBody)
			if resp == nil {
				continue
			}
			l("ass response of POST:", resp)
			l("body response of POST:", resp.Body)
			if error1 != nil {
				l("fucking problem with POSTing an asshole", error1)
				continue
			}
			resp.Body.Close()
		}
	}()

	// responses
	go func() {
		for {
			l("I Unlocked? 1")
			nowi := time.Now()
			myJson := map[string]any{
				"type": "receiver_chunks",
			}

			l("at least this got a pussy")
			jsonData, _ := json.Marshal(myJson)

			requestBody, _ := http.NewRequest("POST", configMap["appscript_url"].(string), bytes.NewBuffer(jsonData))
			requestBody.Header.Set("Content-Type", "application/json")
			requestBody.Host = "script.google.com"
			resp, error1 := client1.Do(requestBody)
			if resp == nil {
				l("this happened but why? connection issues?", error1)
				continue
			}
			l("dick response of POST:", resp)
			l("body response of POST:", resp.Body)

			bytesa, _ := io.ReadAll(resp.Body)
			l("stringfied body response of POST:", string(bytesa))
			if error1 != nil {
				l("fucking problem with POSTing an asshole", error1)
				continue
			}

			location := resp.Header.Get("location")
			if location == "" {
				l("timeout or just rate limit")
				continue
			}
			somepart := location[36:]

			secondReq, _ := http.NewRequest("GET", "https://www.google.com"+somepart, nil)

			secondReq.Host = "script.googleusercontent.com"

			resp, error1 = client1.Do(secondReq)

			l("response of GET:", resp)
			if resp == nil {
				l("adawdadapdpadpapdpap")
				continue
			}
			l("response body of GET:", resp.Body)
			if error1 != nil {
				l("fucking problem with GETing an asshole", error1)
				continue
			}

			bytesa, _ = io.ReadAll(resp.Body)

			l("stringifed response body of GET:", string(bytesa))
			jsoned := map[string][]string{}

			if string(bytesa) == "" {
				l("fuwadiiddidadadawd")
				continue
			} else {
				if string(bytesa) == "timeout" {
					l("is this done?", bool(string(bytesa) == "done"), "is this timeout?", bool(string(bytesa) == "timeout"))
					continue
				}
				l("this isn't null which means we can encode it to json maybe")
				json.Unmarshal(bytesa, &jsoned)
			}

			l("we passed the whole shit now we have the chunk of response")
			rtt_perreq := time.Since(nowi).Milliseconds()
			nowi2 := time.Now()
			for key, value := range jsoned {
				for _, each := range value {
					packet, _ := base64.StdEncoding.DecodeString(each)
					l("id:", key)
					socksmut.Lock()
					if sockets[key].socket != nil { // as i tested before socket when gets closed there's a possibility i get fucked up here
						sockets[key].socket.Write(packet)
						socksmut.Unlock()
					} else {
						socksmut.Unlock()
						l("There..")
						break
					}
				}
			}

			l("It took", rtt_perreq)
			l("After iteration it took", time.Since(nowi2))
			resp.Body.Close()
			l("after jesus")
		}
	}()

	go func() {
		signalc, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
		defer stop()
		<-signalc.Done()
		l("Sending fullclose signal to receiver with google sni...")
		myJson := map[string]any{
			"type": "fullclose",
		}
		jsonData2, _ := json.Marshal(myJson)
		requestBody2, _ := http.NewRequest("POST", configMap["appscript_url"].(string), bytes.NewBuffer(jsonData2))
		requestBody2.Header.Set("Content-Type", "application/json")
		requestBody2.Host = "script.google.com"
		_, error1 := client1.Do(requestBody2)
		if error1 != nil {
			l("fucking problem with POSTing an asshole", error1)
		}
		l("Done")
		syscall.Exit(0)
	}()

	for {
		client, _ := listener.Accept()
		go func() {
			handleClient(client)
		}()
	}
}
