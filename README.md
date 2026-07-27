# Streaming over HTTP1 PROXY

![diagram](images/diagram.png)

# Explanation
This SOCKS5 proxy gathers your clients chunks every 0.5 second and batch them all in one request to google app script endpoint
It has n listeners with 6minute timeout that are waiting for response batch from redis database (they all perform redis LPOP with timeouts)
Every listener has its own redis database, for using it for a day without getting rate limited according to google documentation we can have 20000
Fetch calls a day
All of requests we use (for client chunks and vpn server chunks) are SNI requests

### Some math
for sending chunks if we say each 2 second sends a batch then each second send 2 batch, then for each hour we use 2*3600 = 7200 fetch calls, things got changed for listeners, you will attach the listener after each request, each time you send a request batch your database
endpoint and list for being used gets changed, saying how many lists (j) you need means how many times you want use a single endpoint
well if we say j = 2 (0,1,2 lists 3 lists) and n = 3 (0,1,2,3 databases) you can have at maximum 4*3 = 12 listeners, each listener has a 10s timeout, it does 5 UrlFetchApp.fetch() each 2s and after that listeners goes off

# TODO
1. If project gets some attention i will change the bandwidth and intervals with rtt measurements
2. Make logs better
3. Fixing bugs
4. Copy gstatic ip list that MasterHttpRelayVPN tested them and use them because why i should gather my list (there [ips](https://www.gstatic.com/ipranges/goog.json))

# Known Issues
Udp is kinda broken, i couldn't test it better, but some udp chunks could get transfered successfully but others couldn't because vpn server didn't support Ipv6 and some destinations didnt work (Im talking about this workflows)

# Why i made this?
I got inspired by [MasterHttpRelayVPN](https://github.com/masterking32/MasterHttpRelayVPN) its using SNI for fetching requests 
I first made a vpn with just redis db as a message bus but i needed to pay money for buying a database, so i decided to pay
Nothing and made this, I made this shit alone definetly its buggy, If you're interested u can help me with contributing in this project,
This project is useful when Internet in Iran is whitelisted again and google is open and you need tcp tunneling (udp should work but i need proper infrastructure and proper links for testing that too, its possible protocol is implemented correctly but ipv6 and many other destinations arent working in VPN SERVER)
NO FUCKING AI IS USED THERE

# FA 
سگ وحشی