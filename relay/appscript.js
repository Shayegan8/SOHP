function doPost(e) {
  const rawBody = JSON.parse(e.postData.contents)
  const ids = rawBody.ids
  const type = rawBody.type
  const rtt = rawBody.rtt
  const n = rawBody.n
  const upstashes = [ // we need 4
    { upstashURL: "url", upstashAuthKey: "key" }
  ]

  switch (type) {
    case "client_chunks":
      UrlFetchApp.fetch(`${upstashes[n].upstashURL}/rpush/gListener`, {
        method: "POST",
        headers: {
          "Authorization": `Bearer ${upstashes[n].upstashAuthKey}`,
          "Cache-Control": "no-store"
        },
        payload: JSON.stringify({
          ids: ids
        })
      })
      return ContentService.createTextOutput("adawdwawda").setMimeType(ContentService.MimeType.TEXT)
    case "receiver_chunks":
      const now = Date.now()
      while ((Date.now() - now) < 360000) {
        const respisdj = UrlFetchApp.fetch(`${upstashes[n].upstashURL}/lpop/receiver_chunks`, {
          method: "GET",
          headers: {
            "Authorization": `Bearer ${upstashes[n].upstashAuthKey}`,
            "Cache-Control": "no-store"
          },
          muteHttpExceptions: true,
        })
        const content = respisdj.getContentText()
        const validArray = JSON.parse(content).result

        if (!validArray) { // {"result": null}
          Utilities.sleep(500)
          continue
        }
        return ContentService.createTextOutput(validArray).setMimeType(ContentService.MimeType.TEXT)
      }
      return ContentService.createTextOutput("timeout").setMimeType(ContentService.MimeType.TEXT)
    case "fullclose": // for full close we use first redis url, then with that we make all sockets closed, then we remove receiver_chunks list from all shitties
      UrlFetchApp.fetch(`${upstashes[0].upstashURL}/rpush/gListener`, {
        method: "POST",
        headers: {
          "Authorization": `Bearer ${upstashes[0].upstashAuthKey}`,
          "Cache-Control": "no-store"
        },
        payload: JSON.stringify({
          close: true
        })
      })
      return ContentService.createTextOutput(type).setMimeType(ContentService.MimeType.TEXT)
    case "rtt":
      UrlFetchApp.fetch(`${upstashes[0].upstashURL}/rpush/gListener`, {
        method: "POST",
        headers: {
          "Authorization": `Bearer ${upstashes[0].upstashAuthKey}`
        },
        payload: JSON.stringify({
          rtt: rtt
        })
      })
      return ContentService.createTextOutput("zzzzzz").setMimeType(ContentService.MimeType.TEXT)
    default:
      return ContentService.createTextOutput("problem").setMimeType(ContentService.MimeType.TEXT)
  }
}
