-- openccu-lite: lighttpd's answer while occulited does not answer (at boot, during a restart,
-- after a crash). Attached with magnet.attract-response-start-to to the requests conf.d/occulited.conf
-- proxies to occulited, it runs when the response starts.
--
-- It changes only lighttpd's own proxy error: a 502, 503 or 504 without a Content-Length and with
-- no Content-Type other than HTML, which is what mod_proxy produces when the connection to
-- 127.0.0.1:8183 is refused. Every answer occulited gives itself - its own 503s included - carries
-- a length or a JSON type and passes through unchanged.
--
-- Browsers (an Accept with text/html, outside /api/) get the waiting page: /var/etc/occulite-starting.html,
-- which lighttpd's start fills in from /etc/lighttpd/occulite-starting.html. It polls the health route
-- and reloads the URL the user asked for, since it is served under that URL. The API gets
-- {"error":"starting",...}, anything else a line of text. Always 503, Retry-After: 5 and
-- Cache-Control: no-store, so no browser or proxy keeps the page.
local r = lighty.r
local status = r.req_item.http_status
if status ~= 502 and status ~= 503 and status ~= 504 then
  return 0
end
if r.resp_header["Content-Length"] then
  return 0
end
local ct = r.resp_header["Content-Type"]
if ct and not string.find(ct, "^text/html") then
  return 0
end

local path = r.req_attr["uri.path"] or "/"
local accept = r.req_header["Accept"] or ""
r.resp_header["Cache-Control"] = "no-store"
r.resp_header["Retry-After"] = "5"

if string.find(path, "^/api/") then
  r.resp_header["Content-Type"] = "application/json"
  r.resp_body.set({ '{"error":"starting","message":"occulited is not answering yet"}\n' })
  return 503
end
if not string.find(accept, "text/html", 1, true) then
  r.resp_header["Content-Type"] = "text/plain; charset=utf-8"
  r.resp_body.set({ "occulited is not answering yet\n" })
  return 503
end

local body
for _, file in ipairs({ "/var/etc/occulite-starting.html", "/etc/lighttpd/occulite-starting.html" }) do
  local f = io.open(file, "rb")
  if f then
    body = f:read("*a")
    f:close()
    if body and body ~= "" then
      break
    end
  end
end
if not body or body == "" then
  body = '<!doctype html><meta charset="utf-8"><meta http-equiv="refresh" content="10">'
    .. '<title>openccu-lite</title><p>openccu-lite is starting …</p>'
end
r.resp_header["Content-Type"] = "text/html; charset=utf-8"
r.resp_body.set({ body })
return 503
