package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"slices"
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
	typ     string
	ip      string
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
	areq := make([]byte, 4048)
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
	var sex string
	switch areq[3] { // ATYP
	case 0x01: // IPv4
		ip := net.IP(areq[4:8])
		dstaddr = ip.String()
		portoffset = 8
		sex = "v4"
	case 0x03: // DOMAINNAME
		log.Println("DOMAIN NAME!")
		numberOfBytes := areq[4]
		log.Println("number of octets", numberOfBytes)
		log.Println("the damn thing", string(areq[5:numberOfBytes+5]))
		dstaddr = string(areq[5 : numberOfBytes+5])
		portoffset = 5 + numberOfBytes
		sex = "domain"
	case 0x04: // IPv6
		ip := net.IP(areq[4:20])
		dstaddr = ip.String()
		portoffset = 20
		sex = "v6"
	}
	dstport = binary.BigEndian.Uint16(areq[portoffset : portoffset+2])
	log.Println("FUCKING PORT", dstport)
	id := uuid.New().String()

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

		l("fucking id", id)

		now := time.Now()
		for {
			reader := bufio.NewReader(client)
			reader.Peek(1)
			data, _ := reader.Peek(reader.Buffered())

			l("Request size is:", len(data))
			l("Encoded Request:", base64.StdEncoding.EncodeToString(data))
			socksmut.Lock()
			sockets[id] = SockDetail{client, dstaddr, dstport, "tcp", sex}
			socksmut.Unlock()
			if len(data) == 0 {
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
	case 0x03:
		l("UDPPPPPP")
		udpShit, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("0.0.0.0"), Port: 0})
		response := make([]byte, 10)
		response[0] = 0x05 // VER
		response[1] = 0x00 // REP
		response[2] = 0x00 // RSV
		response[3] = 0x01 // ATYP
		// many clients dont give a fuck about server bound ip/port
		response[4], response[5], response[6], response[7] = 0, 0, 0, 0
		port := uint16(udpShit.LocalAddr().(*net.UDPAddr).Port)
		binary.BigEndian.PutUint16(response[8:], port)

		_, err := client.Write(response)
		if err != nil {
			l("Problem with writing this out continuing...")
			client.Close()
			udpShit.Close()
			return
		}
		l("We wrote the response here")

		for {
			data := make([]byte, 4048)
			n, _, err := udpShit.ReadFromUDP(data)
			l("Somehow we read a shit from udp?")
			if err != nil {
				l("Error while reading packet from udp client,", err)
				client.Close()
				udpShit.Close()
				break
			}
			data = data[:n]
			rsv := binary.BigEndian.Uint16(data)
			if rsv != 0x0000 {
				l("Bad rsv,", rsv)
				client.Close()
				udpShit.Close()
				continue
			}
			frag := data[2]
			if frag != 0x00 {
				l("FRAG is shitty,", frag)
				continue
			}
			var dstaddr1 string
			var portoffset1 uint8
			var jerk string
			switch data[3] { // atyp
			case 0x01: // IPv4
				l("FUCKING IPv4")
				ip := net.IP(data[4:8])
				dstaddr1 = ip.String()
				portoffset1 = 8
				jerk = "v4"
			case 0x03: // DOMAINNAME
				log.Println("DOMAIN NAME!")
				numberOfBytes := data[4]
				log.Println("number of octets", numberOfBytes)
				log.Println("the damn thing", string(data[5:numberOfBytes+5]))
				dstaddr1 = string(data[5 : numberOfBytes+5])
				portoffset1 = 5 + numberOfBytes
				jerk = "domain"
			case 0x04: // IPv6
				l("FUCKING IPV6")
				ip := net.IP(data[4:20])
				dstaddr1 = ip.String()
				portoffset1 = 20
				jerk = "v6"
			}
			l("DATA for", dstaddr1, ":", hex.EncodeToString(data))

			fuckingP := binary.BigEndian.Uint16(data[portoffset1:])
			actual_data := data[portoffset1+2:]
			socksmut.Lock()
			l("PUT ON MAP")
			sockets[id] = SockDetail{udpShit, dstaddr1, fuckingP, "udp", jerk}
			socksmut.Unlock()

			request := base64.StdEncoding.EncodeToString(actual_data)
			l("request encoded")
			reqmut.Lock()
			requests = append(requests, Request{id, request})
			reqmut.Unlock()

			log.Println("udp FUCKING PORT", fuckingP)
		}
	}
}

var endpoints_mut = &sync.Mutex{}
var endpoints_amut = &sync.Mutex{}
var endpoints_flag bool = false

type Respnl struct {
	Result []json.RawMessage `json:"result"`
}

func endpoint_chunks(client1 *http.Client, endpoints []string, j int64) {
	once := &sync.Once{}
	secturk := int64(configMap["sector"].(float64))
	chanjerk := make(chan any, j*secturk)
	l("secturkkkk:", j*secturk)
	checkmut := &sync.Mutex{}
	var checker int64 = 0
	endpoints_amut.Lock()
	endpoints_c := endpoints
	endpoints_amut.Unlock()
	for num, endpoint := range endpoints_c {
		if int64(num) == secturk+1 {
			break
		}
		mut := &sync.Mutex{}
		numj := int64(configMap["dbs"].(float64))
		for nj := range j + 1 {
			l("j++++1")
			thereonce := &sync.Once{}
			go func() {
				for {
					select {
					case <-chanjerk:
						runtime.Goexit()
					default:
						l("resp Goroutine")
						nowi := time.Now()
						mut.Lock()
						l("which database it uses?", numj)
						l("which list it uses?", nj)
						myJson := map[string]any{
							"n":    numj,
							"type": "receiver_chunks",
							"j":    nj,
						}
						if numj != 0 {
							numj--
						}
						mut.Unlock()

						jsonData, _ := json.Marshal(myJson)

						requestBody, _ := http.NewRequest("POST", endpoint, bytes.NewBuffer(jsonData))
						requestBody.Header.Set("Content-Type", "application/json")
						requestBody.Host = "script.google.com"
						resp, error1 := client1.Do(requestBody)
						if resp == nil {
							l("this happened but why? connection issues?", error1)
							continue
						}
						l("resp dick response of POST:", resp)
						l("resp body response of POST:", resp.Body)

						bytesa, _ := io.ReadAll(resp.Body)
						l("resp stringfied body response of POST:", string(bytesa))
						if error1 != nil {
							l("fucking problem with POSTing an asshole", error1)
							continue
						}

						location := resp.Header.Get("location")
						if location == "" {
							l("resp timeout or just rate limit")
							thereonce.Do(func() {
								checkmut.Lock()
								checker++
								checkmut.Unlock()
							})
							if checker == j*secturk {
								once.Do(func() {
									copy_endpoints := make([]string, len(endpoints_c)-3)
									endpoints_amut.Lock()
									for i := range endpoints {
										if len(endpoints) < i+3 {
											copy_endpoints[i] = endpoints[i+3]
										}
									}

									if slices.Compare(copy_endpoints, endpoints) == 0 {
										endpoints_amut.Unlock()
										return
									}
									endpoints = copy_endpoints
									endpoints_amut.Unlock()

									for range secturk * j {
										l("secturkation")
										chanjerk <- []byte{}
									}
									endpoint_chunks(client1, endpoints, j)
								})
								break
							} else {
								continue
							}
						}
						somepart := location[36:]

						secondReq, _ := http.NewRequest("GET", "https://www.google.com"+somepart, nil)

						secondReq.Host = "script.googleusercontent.com"

						resp, error1 = client1.Do(secondReq)

						l("response of GET:", resp)
						if resp == nil {
							l("resp adawdadapdpadpapdpap")
							continue
						}
						l("response body of GET:", resp.Body)
						if error1 != nil {
							l("resp fucking problem with GETing an asshole", error1)
							continue
						}

						bytesa, _ = io.ReadAll(resp.Body)
						l("stringifed response body of GET:", string(bytesa))
						strbody := []rune(string(bytesa))

						if string(bytesa) == "timeout" {
							l("is this done?", bool(string(bytesa) == "done"), "is this timeout?", bool(string(bytesa) == "timeout"))
							endpoints_mut.Lock()
							endpoints_flag = false
							endpoints_mut.Unlock()
							runtime.Goexit()
						}

						if strbody[0] == '[' { // means its a list of shits
							l("sexjerk")
							l("print it:", string(bytesa))
							jsoned := []map[string]map[string][]string{}
							json.Unmarshal(bytesa, &jsoned)
							for _, each := range jsoned {
								get := each["result"]
								for key, value := range get {
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
							}
						} else { // means not
							l("not sexjerk")
							jsoned := map[string][]string{}
							json.Unmarshal(bytesa, &jsoned)
							get := jsoned["result"]
							getb, _ := json.Marshal(get)
							var unmar []map[string][]string
							json.Unmarshal(getb, &unmar)
							l("print the damn", string(getb))
							l("print the fucking map", unmar)
							for _, each := range unmar {
								l("now print the fucking map", each)
								for key, value := range each {
									for _, vv := range value {
										packet, _ := base64.StdEncoding.DecodeString(vv)
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
							}

						}

						l("we passed the whole shit now we have the chunk of response")
						rtt_perreq := time.Since(nowi).Milliseconds()
						nowi2 := time.Now()

						l("resp It took", rtt_perreq)
						l("resp After iteration it took", time.Since(nowi2))
						resp.Body.Close()
						l("resp after jesus")
					}
				}
			}()
		}
	}
}

func send_chunks(ids map[string]map[string]any, endpoints []string, client1 *http.Client, fuck_number int64, shash_number int64, sex_number int64) {

	myJson := map[string]any{
		"ids":  ids,
		"type": "client_chunks",
		"n":    fuck_number,
	}

	jsonData, _ := json.Marshal(myJson)
	endpoints_amut.Lock()
	requestBody, _ := http.NewRequest("POST", endpoints[shash_number], bytes.NewBuffer(jsonData))
	if shash_number != 0 { // which endpoint
		shash_number--

		if shash_number == 0 {
			shash_number = int64(len(configMap["appscript_urls"].([]any)) - 1)
		}
	}
	endpoints_amut.Unlock()

	requestBody.Header.Set("Content-Type", "application/json")
	requestBody.Host = "script.google.com"
	resp, error1 := client1.Do(requestBody)
	if resp == nil {
		l("response is null? req")
		//go retry(client1, endpoints, ids, fuck_number, 0)

		copy_endpoints := make([]string, len(endpoints)-3)
		endpoints_amut.Lock()
		for i := range endpoints {
			if len(endpoints) < i+3 {
				copy_endpoints[i] = endpoints[i+3]
			}
		}

		if slices.Compare(copy_endpoints, endpoints) == 0 {
			endpoints_amut.Unlock()
			return
		}
		endpoints = copy_endpoints
		endpoints_amut.Unlock()
		send_chunks(ids, endpoints, client1, fuck_number, shash_number, sex_number)
		if sex_number != 0 { // which list within database
			sex_number--

			if sex_number == 0 {
				sex_number = int64(configMap["j"].(float64))
			}
		}
		if fuck_number != 0 { // which database
			fuck_number--

			if fuck_number == 0 {
				fuck_number = int64(configMap["dbs"].(float64))
			}
		}
		return
	}
	l("ass response of POST req:", resp)

	location := resp.Header.Get("location")
	if location == "" {
		l("REEEQ timeout or just rate limit")
		//go retry(client1, endpoints, ids, fuck_number, 0)
		copy_endpoints := make([]string, len(endpoints)-3)
		endpoints_amut.Lock()
		for i := range endpoints {
			if len(endpoints) < i+3 {
				copy_endpoints[i] = endpoints[i+3]
			}
		}

		if slices.Compare(copy_endpoints, endpoints) == 0 {
			endpoints_amut.Unlock()
			return
		}
		endpoints = copy_endpoints
		endpoints_amut.Unlock()
		send_chunks(ids, endpoints, client1, fuck_number, shash_number, sex_number)

		return
	}
	bod, _ := io.ReadAll(resp.Body)
	l("body response of POST request:", string(bod))
	if error1 != nil {
		l("fucking problem with POSTing an asshole", error1)
		return
	}
	resp.Body.Close()

	endpoints_mut.Lock()
	if !endpoints_flag {
		endpoints_flag = true
		l("attatching the shit")
		endpoint_chunks(client1, endpoints, sex_number)
	}
	endpoints_mut.Unlock()
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
		case "--resolve", "-r": // i'll use someone's else list
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
	listener, error1 := net.Listen("tcp", "0.0.0.0:12345")
	if error1 != nil {
		log.Println(`Port is in use maybe`, error1)
	}

	ticker := time.NewTicker(2000 * time.Millisecond)

	fuck_number := int64(configMap["dbs"].(float64)) // for redis h scale
	sex_number := int64(configMap["j"].(float64))
	shash_number := int64(len(configMap["appscript_urls"].([]any)) - 1)
	l("FUCKING FUCK NUMBER:", shash_number)
	endpoints := make([]string, shash_number+1)

	if valu, ok := configMap["appscript_urls"].([]any); ok {
		for i, v := range valu {
			l("sss")
			l(v.(string))
			endpoints[i] = v.(string)
		}
	}

	//my chunks
	go func() {
		for {
			<-ticker.C
			reqmut.Lock()
			if len(requests) == 0 {
				reqmut.Unlock()
				continue
			} else {
				l("seex")
			}
			ids := map[string]map[string]any{}
			l("the reqs:", requests[:])
			for _, value := range requests[:] {
				l("req PRINT THE DAMN,", ids[value.id])
				if _, ok := ids[value.id]; !ok {
					socksmut.Lock()
					shitMap[value.id] = sockets[value.id]
					l("PRINT THE SHIT OUT OF IT", shitMap[value.id])
					socksmut.Unlock()
					l("first jerk")
					ids[value.id] = map[string]any{
						"data":    []string{value.request},
						"dstaddr": shitMap[value.id].dstaddr,
						"dstport": shitMap[value.id].dstport,
						"typ":     shitMap[value.id].typ,
						"ip":      shitMap[value.id].ip,
						"j":       sex_number,
					}
				} else {
					l("sec jerk")
					n := append(ids[value.id]["data"].([]string), value.request)
					ids[value.id] = map[string]any{
						"data":    n,
						"dstaddr": shitMap[value.id].dstaddr,
						"dstport": shitMap[value.id].dstport,
						"typ":     shitMap[value.id].typ,
						"ip":      shitMap[value.id].ip,
						"j":       sex_number,
					}
				}
			}
			requests = nil
			reqmut.Unlock()

			send_chunks(ids, endpoints, client1, fuck_number, shash_number, sex_number)
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
		endpoints_amut.Lock()
		l(endpoints[shash_number])
		requestBody2, _ := http.NewRequest("POST", endpoints[shash_number], bytes.NewBuffer(jsonData2))
		endpoints_amut.Unlock()

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
