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
}

var sockets map[string]SockDetail = map[string]SockDetail{}

type Request struct {
	id      string
	request string
}

type Requests struct {
	mutex    sync.Mutex
	requests []Request
}

var requests Requests = Requests{requests: []Request{}}

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

		id := uuid.New().String()
		sockets[id] = SockDetail{client, dstaddr, dstport}
		l("fucking id", id)

		cChan := make(chan any, 1)
		now := time.Now()
		go func() {
			for {
				reader := bufio.NewReader(client)
				reader.Peek(1)
				data, _ := reader.Peek(reader.Buffered())
				l("Request size is:", len(data))
				l("Encoded Request:", base64.StdEncoding.EncodeToString(data))
				reader.Discard(len(data)) // this not required
				if string(data) == "" {   // as i tested i never see err
					close(cChan)
					l("time took is:", time.Since(now))
					l("End of file")
					myJson := map[string]any{
						"id":   id,
						"type": "close",
					}
					jsonData, _ := json.Marshal(myJson)
					requestBody, _ := http.NewRequest("POST", configMap["appscript_url"].(string), bytes.NewBuffer(jsonData))
					requestBody.Header.Set("Content-Type", "application/json")
					requestBody.Host = "script.google.com"
					resp, error1 := client1.Do(requestBody)
					if error1 != nil {
						l("fucking problem with POSTing an asshole", error1)
					}

					l("Closing")
					resp.Body.Close()
					l("Close happened")
					break
				}
				request := base64.StdEncoding.EncodeToString(data)
				l("request encoded")
				requests.requests = append(requests.requests, Request{id, request})
				l("request pushed")

			}
		}()

	loop:
		for {
			select {
			case <-cChan:
				l("if this got notified")
				break loop
			default:
				select {
				case <-cChan:
					break loop
				default:
				}
				nowi := time.Now()
				var myJson = map[string]any{
					"type": "receiver_chunks",
				}
				l("at least this got a pussy")
				jsonData, _ := json.Marshal(myJson)

				requestBody, _ := http.NewRequest("POST", configMap["appscript_url"].(string), bytes.NewBuffer(jsonData))
				requestBody.Header.Set("Content-Type", "application/json")
				requestBody.Host = "script.google.com"
				select {
				case <-cChan:
					break loop
				default:
				}
				resp, error1 := client1.Do(requestBody)
				select {
				case <-cChan:
					break loop
				default:
				}
				if resp == nil {
					l("this happened but why? connection issues?", error1)
					break
				}
				l("dick response of POST:", resp)
				l("body response of POST:", resp.Body)
				select {
				case <-cChan:
					break loop
				default:
				}
				bytesa, _ := io.ReadAll(resp.Body)
				select {
				case <-cChan:
					break loop
				default:
				}

				l("stringfied body response of POST:", string(bytesa))
				if error1 != nil {
					l("fucking problem with POSTing an asshole", error1)
					break
				}

				location := resp.Header.Get("location")
				if location == "" {
					l("MAYBE THIS IS RATE LIMIT BUT WE BREAK THE SHIT OUT OF THIS")
					break
				}
				somepart := location[36:]

				secondReq, _ := http.NewRequest("GET", "https://www.google.com"+somepart, nil)

				secondReq.Host = "script.googleusercontent.com"
				select {
				case <-cChan:
					break loop
				default:
				}

				resp, error1 = client1.Do(secondReq)
				select {
				case <-cChan:
					break loop
				default:
				}

				l("response of GET:", resp)
				l("response body of GET:", resp.Body)
				if error1 != nil {
					l("fucking problem with GETing an asshole", error1)
					break
				}
				select {
				case <-cChan:
					break loop
				default:
				}

				bytesa, _ = io.ReadAll(resp.Body)

				select {
				case <-cChan:
					break loop
				default:
				}

				l("stringifed response body of GET:", string(bytesa))
				jsoned := map[string][]string{}
				if string(bytesa) != "null" {
					l("this isn't null which means we can encode it to json maybe")
					json.Unmarshal(bytesa, &jsoned)
				} else {
					l("response chunk is null whiich means we should retry or maybe?") // i dont i should break or continue?
					continue
				}
				l("we passed the whole shit now we have the chunk of response")
				rtt_perreq := time.Since(nowi).Milliseconds()
				nowi2 := time.Now()
				for key, value := range jsoned {
					for _, each := range value {
						packet, _ := base64.StdEncoding.DecodeString(each)
						l("id:", id)
						sockets[key].socket.Write(packet)
					}
				}
				l("It tooken", rtt_perreq)
				l("After iteration it tooken", time.Since(nowi2))
				resp.Body.Close()
				go func() {
					l("Sending fucking rtt")
					var myJson = map[string]any{
						"id":   id,
						"rtt":  rtt_perreq,
						"type": "rtt",
					}
					jsonData, _ := json.Marshal(myJson)

					requestBody, _ := http.NewRequest("POST", configMap["appscript_url"].(string), bytes.NewBuffer(jsonData))
					requestBody.Header.Set("Content-Type", "application/json")
					requestBody.Host = "script.google.com"
					resp, error1 := client1.Do(requestBody)
					if resp == nil {
						l("aaa this happened but why? connection issues?", error1)
					}
					resp.Body.Close()
					l("shit jerk")
				}()
			}
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

	ticker := time.NewTicker(500 * time.Millisecond)

	go func() {
		for {
			<-ticker.C
			requests.mutex.Lock()
			var batch int
			leni := len(requests.requests)
			if leni == 0 {
				requests.mutex.Unlock()
				continue
			} else if leni >= 30 {
				batch = 30
			} else {
				batch = leni
			}
			ids := map[string]map[string]any{}

			for _, value := range requests.requests[:batch] {
				detail := sockets[value.id]
				if _, ok := ids[value.id]; !ok {
					l("first jerk")
					ids[value.id] = map[string]any{
						"data":    []string{value.request},
						"dstaddr": detail.dstaddr,
						"dstport": detail.dstport,
					}
				} else {
					l("sec jerk")
					n := append(ids[value.id]["data"].([]string), value.request)
					ids[value.id] = map[string]any{
						"data":    n,
						"dstaddr": detail.dstaddr,
						"dstport": detail.dstport,
					}
				}
			}
			requests.requests = requests.requests[batch:]
			requests.mutex.Unlock()

			var myJson = map[string]any{
				"ids":  ids,
				"type": "client_chunks",
			}

			l("RESULT: ", ids)
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

	go func() {
		signalc, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
		defer stop()
		<-signalc.Done()
		l("Sending fullclose signal to receiver with google sni...")
		myJson := map[string]any{
			"type": "fullclose",
		}
		jsonData, _ := json.Marshal(myJson)
		requestBody, _ := http.NewRequest("POST", configMap["appscript_url"].(string), bytes.NewBuffer(jsonData))
		requestBody.Header.Set("Content-Type", "application/json")
		requestBody.Host = "script.google.com"
		_, error1 := client1.Do(requestBody)
		if error1 != nil {
			l("fucking problem with POSTing an asshole", error1)
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
