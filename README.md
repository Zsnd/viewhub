# ViewHub

ViewHub combines device enrollment, live video, recording playback and PTZ across isolated customer spaces. The cloud uses Go, React and SQLite. Site gateways use Go, go2rtc, MQTT 5 over WSS for commands and a separate WSS connection for relayed media.

| Connection | Live | History | PTZ |
| --- | --- | --- | --- |
| GB28181 camera or recorder | Cloud WebRTC, then compatible browser fallback | GB28181 RecordInfo and playback, subject to recorder support | GB28181, enable per supported channel |
| Lightweight edge gateway | go2rtc WebRTC direct, then cloud relay | ONVIF Profile G, then Hikvision ISAPI where configured | ONVIF |
| Frigate | go2rtc WebRTC direct, then cloud relay | Frigate HTTP API and existing local recordings | ONVIF when configured |

The monitor wall persists channel assignments per account. Choose 4, 9 or 16 tiles. Fullscreen shows the video grid, and exiting retains the layout. History uses the site's timezone.

## Deployment

- Cloud: set `.env` from `.env.example`. Create `secrets/web.password`, `secrets/sites.json` and `secrets/mosquitto.passwd`, then run `docker compose up -d`. Route the public hostname with `deploy/viewhub.caddy`. Keep HTTP and the broker WebSocket port bound to loopback.
- Site: fill `deploy/channels.local.json` and `deploy/go2rtc.local.yaml` using the examples. Use a unique site ID, MQTT password and media token. Run `docker compose -f compose.edge.yaml up -d`. If connecting to a Frigate Compose installation, include `-f deploy/frigate.compose.yaml` and set `VIEWHUB_FRIGATE=http://frigate:5000`.
- Provision lightweight gateways outside the end-user interface. The authenticated deployment endpoint `POST /api/edge` takes a gateway name and recorder subnet and returns an installation Compose file for the current space. On a Linux AMD64 or ARM64 box with Compose 2.23.1 or later, run `docker compose up -d --build` in that directory. The file contains the box credentials. The cloud serves the edge binary and automatically provisions the scoped MQTT account.
- Existing gateways configured through `secrets/sites.json` and the static broker password file remain supported. Frigate sites use their own HTTP recording API.
- Public cloud ports: HTTPS 443/TCP (and 80/TCP for HTTP redirect/ACME), SIP 5060/TCP and UDP, the configured GB RTP UDP range, and cloud WebRTC 8191/UDP. MQTT and the media tunnel use the same HTTPS host. Site WebRTC uses the configured ICE candidates. UDP connectivity depends on the two endpoint networks.

## Recorder playback over ONVIF

Use a Profile G recorder for vendor-neutral recording search and replay. Enable ONVIF on the recorder and create an account with recording search and replay access. On the site gateway, set `VIEWHUB_ONVIF_LAN` to the site's recorder subnet (for example, `10.88.1.0/24`) and recreate the edge service with Compose. The `recorder-data` volume stores the configuration on the gateway. No public recorder port is needed.

Open **设备接入 → 添加录像机**. ViewHub uses the online lightweight gateway automatically if there is only one. Then enter the recorder's LAN IPv4 address, ONVIF port and account. Use **检测录像通道**, select a recording and its video track. ViewHub matches its ONVIF video source to the recorder’s media profile when the recorder exposes a shared token. You can save one channel with live video and history, bind history to an existing live channel, or save history only. Go to **视频工作台 → 历史录像** to select a date and seek on the timeline. To remove a binding, open the site's channel list and choose **移除录像来源**.

For an installation managed through files, the edge channel configuration in `deploy/edge.example.json` shows the ONVIF Device Service URL, credentials and the recording token assigned to that channel. Set `track` when the recording has several video tracks. When there is only one, ViewHub selects it through the standard ONVIF recording information request. The optional `profile` field is for PTZ.

An explicitly configured ONVIF recording takes precedence over the Frigate adapter. When Profile G reports an unsupported operation, a configured Hikvision ISAPI adapter is used as a fallback. Frigate's HTTP/HLS path remains available for Frigate-only channels. The timeline shows the source that answered the query.

Profile G playback requests an absolute time from the recorder over RTSP. ViewHub sends H.264 over WebRTC when ICE establishes a direct path, or relays fragmented MP4 through the cloud. The site exposes UDP 18558 for replay ICE when reachable. Set VIEWHUB_REPLAY_ICE_IP to the address the browser can reach if the edge container needs an explicit host candidate. Frigate uses HLS. See [the replay design and acceptance criteria](EDGE-REPLAY.md).

## Local verification

Run `go test ./...` and `npm ci && npm run build` inside `web`. Use separate ports and credentials for testing next to an existing gateway. Back up SQLite and the secrets directory before upgrades. Frigate recordings stay on the Frigate installation.

