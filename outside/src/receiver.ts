import net from 'net'
import dgram from 'dgram'
import dns from 'dns/promises'
import { Redis } from 'ioredis'
import { exit } from 'process'
import PQueue from 'p-queue'

function logger(param: string, type?: string) {
    console.log(type == "info" ? `${new Date().toISOString()} [\x1b[33mINFO\x1b[0m] ${param}`
        : (type == "error" ? `${new Date().toISOString()} [\x1b[31mERR\x1b[0m] ${param}` : param))
}

//DNS RESOLVE, for now its not perfect but works atleast
const workingDNSes = new Map<string, { ip: string, requiredTime: number }>() // Map<address, ip>
let fastestDNSes = new Map<string, { ip: string, requiredTime: number }>() //Map<address, fastest ip>

async function testConnection(address: string, ip: string, port: number, typ: string): Promise<boolean> {
    logger("testing connection for " + ip)
    return await new Promise<boolean>((resolve) => {
        const startTime = Date.now()
        let connection: net.Socket | dgram.Socket
        if (typ == "tcp") {
            logger("Here")
            connection = net.createConnection(port, ip)
            const timeo = setTimeout(() => {
                (connection as net.Socket).destroy();
                resolve(false)
            }, 3000)
            connection.once('connect', () => {
                logger("Im finally connected and there's no problem with stupid dns")
                clearTimeout(timeo)
                workingDNSes.set(address, { ip: ip, requiredTime: Date.now() - startTime })
                resolve(true)
            })
            connection.once('error', () => {
                clearTimeout(timeo);
                (connection as net.Socket).destroy()
                resolve(false)
            })
        } else {
            logger("UDPPPPPPP? " + typ)
            workingDNSes.set(address, { ip: ip, requiredTime: Date.now() - startTime }) // bs
            resolve(true)
        }
    })
}

async function getFastestIP(address: string, port: number, typ: string): Promise<string | null> {
    logger("fastest dns")
    if (fastestDNSes.has(address))
        return fastestDNSes.get(address)!.ip
    try {
        const ips = (await dns.resolve(address))
        if (ips.length == 0)
            return null
        for (const ip of ips) {
            logger("address: " + ip)
            await testConnection(address, ip, port, typ)
        }
        fastestDNSes = new Map([...workingDNSes.entries()].sort((a, b) => a[1].requiredTime - b[1].requiredTime))
        return fastestDNSes.get(address)?.ip || null
    } catch (e) {
        logger(`dns on reject for sake of pork ${e}`)
        return null
    }
}

const connDetails = new Map<string, { socket: net.Socket | dgram.Socket, dstaddr: string, dstport: number }>()

const connList = [
    {
        redis: new Redis("address", {
            maxRetriesPerRequest: null
        }), address: "address"
    }
];
// this is blocking and maybe its better i create it per connection but its not noticable 

(async () => {
    let i = 0
    for (const redis of connList) {
        i++
        let j = i;
        (async () => {
            const blockingConn = new Redis(redis.address, {
                maxRetriesPerRequest: null
            })
            while (true) {
                try {
                    logger("before this blocking popping")
                    const gListener = await blockingConn.blpop("gListener", 0)
                    logger("is this fucked? " + j)
                    logger("full scale: " + gListener)
                    const job = JSON.parse(gListener![1])
                    const { ids, close } = job

                    logger("FUCKING CONN REQUESTS SIZE!!!!")
                    logger("gListener got notified!\n" + gListener)

                    // global backpressure
                    if (typeof close === "boolean") { // close all sockets because client is gone
                        if (close) {
                            logger("at least im here")
                            const copy = new Map(connDetails)
                            for (const [key, obj] of copy.entries()) {
                                try {
                                    (obj.socket as net.Socket).destroy();
                                } catch (e) {
                                    logger("This is udp trying to close with dgram.Socket");
                                    (obj.socket as dgram.Socket).close()
                                }

                                logger(`Signal close for whole chain detected`)
                            }
                            await connList[0]!.redis.del("receiver_chunks")
                            await connList[1]!.redis.del("receiver_chunks")
                            await connList[2]!.redis.del("receiver_chunks")
                            await connList[3]!.redis.del("receiver_chunks")
                            connDetails.clear()
                        }
                        continue
                    }

                    let pressure = 0
                    const reqsReps = new Map<string, string[][]>()
                    const flatAss = new Map()

                    const pq_reqsreps = new PQueue({ concurrency: 1 })

                    Object.entries(ids).forEach(([key, value]) => {
                        const callback = setImmediate(async () => {

                            const usingValue = (value as any)
                            const ipType = usingValue.ip
                            logger("IP TYPE IS:  " + ipType)
                            const typ = usingValue.typ
                            logger(`TYPE IS: ${typ}`)
                            const id = key
                            logger(`KEY IS: ${id}`)
                            const dstaddr = usingValue.dstaddr
                            logger(`DSTADDR IS: ${dstaddr}`)
                            const dstport = usingValue.dstport
                            logger(`DSTPORT IS: ${dstport}`)
                            const data = (usingValue.data as string[])
                            logger(`USINGDATA IS: ${data}`)
                            if (!connDetails.has(id)) {
                                logger("Of course im fucking called")
                                let fastestWorkingIP: string | null
                                if (ipType == "domain") {
                                    fastestWorkingIP = await getFastestIP(dstaddr, dstport, typ)
                                    if (!fastestWorkingIP) {
                                        logger(`There is no working DNS for such an address ${id}`, "error")
                                        return
                                    }
                                } else {
                                    fastestWorkingIP = dstaddr
                                }
                                let appServer: net.Socket | dgram.Socket
                                if (typ == "tcp") {
                                    appServer = net.createConnection(dstport, fastestWorkingIP!)
                                    appServer.setTimeout(10000) // maybe its because of timeout
                                    appServer.on('error', (err) => {
                                        connDetails.delete(id)
                                        //it would be fine if we could remove this id in db
                                        logger("Connection error: " + err.message, "error")
                                        clearImmediate(callback)
                                    })
                                } else {
                                    if (ipType != "v6")
                                        appServer = dgram.createSocket("udp4")
                                    else
                                        appServer = dgram.createSocket("udp6")

                                }

                                connDetails.set(id, { socket: appServer, dstaddr: dstaddr, dstport: dstport })

                                for (const each of data) {
                                    const decodeData = Buffer.from(each, 'base64');
                                    if (typ == "tcp")
                                        (appServer as net.Socket).write(decodeData)
                                    else {
                                        logger(`sending udp packet`);
                                        if (typ == "domain") {
                                            const resvl = await dns.resolve(dstaddr);
                                            (connDetails.get(id)!.socket as dgram.Socket).send(decodeData, dstport, resvl[0])
                                        } else
                                            (appServer as dgram.Socket).send(decodeData, dstport, dstaddr)
                                    }
                                }

                                let buff_arr: string[] = []
                                let fuckingTimeout: NodeJS.Timeout | null = null
                                const handle = (chunk: Buffer) => {
                                    if (fuckingTimeout)
                                        clearTimeout(fuckingTimeout)
                                    logger(`data arrived for ${id}`)
                                    buff_arr.push(chunk.toString('base64'))
                                    logger("Shitting down")
                                    pressure += chunk.length
                                    // critical, put a queue here for doing this op
                                    if (pressure >= 9437184) {
                                        pq_reqsreps.add(() => {
                                            logger(`executing order 67`)
                                            for (const [key, value] of reqsReps) {
                                                flatAss.set(key, value.flat())
                                                reqsReps.delete(key)
                                            }
                                            logger(`Fucking pushed motherfucker 5MB!!!!, ${pressure}`)
                                            pressure = 0
                                            logger(`We are sending batch response 5MB`)

                                            if (flatAss.size != 0) {
                                                const ass = Object.fromEntries(flatAss)
                                                logger("WHAT THE HELL IM SETTING? " + JSON.stringify(ass))
                                                redis.redis.rpush("receiver_chunks", JSON.stringify(ass)).catch((e) => {
                                                    logger(`FUCKING ERROR likely its about size of record, ${e}`)
                                                })
                                                flatAss.clear()
                                            } else
                                                logger(`HOW THE FUCK THIS SHIT IS 0`)
                                        })
                                    }
                                    fuckingTimeout = setTimeout(() => {
                                        logger("This happened, " + buff_arr.length)
                                        const getjerk = reqsReps.get(id)
                                        if (getjerk) {
                                            logger(`This fucked`)
                                            getjerk.push(buff_arr)
                                        } else {
                                            const listjerk: string[][] = [buff_arr]
                                            reqsReps.set(id, listjerk)
                                            logger(`reqsReps size: ${reqsReps.size}`)
                                        }
                                        logger("this happened right?")

                                        pq_reqsreps.add(() => {
                                            for (const [key, value] of reqsReps) {
                                                flatAss.set(key, value.flat())
                                                reqsReps.delete(key)
                                            }
                                            logger(`Fucking pushed motherfucker NOT 5MB!!!!, ${pressure}`)
                                            logger(`We are sending batch response NOT 5MB`)
                                            if (flatAss.size != 0 && reqsReps.size == 0) {
                                                logger("FUCKKKKKK YEAHHHHHH")
                                                const ass = Object.fromEntries(flatAss)
                                                logger("WHAT THE HELL IM SETTING? " + JSON.stringify(ass))
                                                redis.redis.rpush("receiver_chunks", JSON.stringify(Object.fromEntries(flatAss))).catch((e) => {
                                                    logger(`FUCKING ERROR likely its about size of record NOT 5MB, ${e}`)
                                                })
                                                flatAss.clear()
                                            } else
                                                logger(`HOW THE FUCK THIS IS 0 NOT 5MB`)
                                            pressure = 0
                                            buff_arr = []
                                        })
                                        logger("They have same size doing operating")
                                    }, 500)
                                }

                                if (typ == "tcp") {
                                    logger("PUTTING EVENT LISTENER HERE FOR THIS SOCKET");
                                    (appServer as net.Socket).on('data', handle)
                                } else {
                                    logger("Listening from udp");
                                    (appServer as dgram.Socket).on('message', (data: Buffer) => {
                                        logger("FUCKING ARRIVED BITCH UDP")
                                        handle(data)
                                    })
                                }


                            } else {
                                for (const each of data) {
                                    const decodeData = Buffer.from(each, 'base64');
                                    if (typ == "tcp")
                                        (connDetails.get(id)!.socket as net.Socket).write(decodeData)
                                    else {
                                        logger(`sending udp packet`);
                                        if (typ == "domain") { // well i cant fuck with ipv6 in github workflows, and udp dosent work most of the time for something like torrent
                                            const resvl = await dns.resolve4(dstaddr);
                                            (connDetails.get(id)!.socket as dgram.Socket).send(decodeData, dstport, resvl[0])
                                        } else
                                            (connDetails.get(id)!.socket as dgram.Socket).send(decodeData, dstport, dstaddr)
                                    }
                                }
                            }
                        })
                    });

                    logger(`this guy notified for ${job.id}`)
                } catch (e) {
                    logger(`Problem with rpoping values ${e}`)
                }
            }
        })()
    }
})()

const cleanUp = async () => {
    logger("Testing")
    for (let key of connDetails.keys()) {
        try {
            (connDetails.get(key)!.socket as net.Socket).destroy()
        } catch (e) {
            logger("Trying with udp");
            (connDetails.get(key)!.socket as dgram.Socket).close()
        }
        connDetails.delete(key)
        //conn.del("receiver_chunks")
    }
    exit(0)
}

process.on('SIGTERM', cleanUp)
process.on('SIGINT', cleanUp)
