# Streaming over HTTP1 PROXY

![diagram](images/diagram.png)

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
