package media

// Older senders may omit the PS map. A base-layer HEVC VPS has a
// header which is not a valid AVC NAL unit type.
func sniffVideoCodec(nalus [][]byte) byte {
	for _, nal := range nalus {
		if len(nal) >= 3 && nal[0] == 0x40 && nal[1] == 1 {
			return videoHEVC
		}
	}
	for _, nal := range nalus {
		if nal[0]&0x9f == 7 {
			return videoAVC
		}
	}
	return 0
}

type videoParameters struct {
	vps, sps, pps []byte
}

func nalType(codec byte, nal []byte) byte {
	if codec == videoHEVC {
		if len(nal) < 2 || nal[0]&0x80 != 0 || nal[1]&7 == 0 {
			return 255
		}
		return nal[0] >> 1 & 63
	}
	return nal[0] & 31
}

func (p *videoParameters) inspect(codec byte, nalus [][]byte) bool {
	key := false
	for _, nal := range nalus {
		kind := nalType(codec, nal)
		if codec == videoHEVC {
			switch kind {
			case 32:
				p.vps = nal
			case 33:
				p.sps = nal
			case 34:
				p.pps = nal
			case 16, 17, 18, 19, 20, 21:
				key = true
			}
		} else {
			switch kind {
			case 5:
				key = true
			case 7:
				p.sps = nal
			case 8:
				p.pps = nal
			}
		}
	}
	return key
}

func (p videoParameters) complete(codec byte) bool {
	return len(p.sps) != 0 && len(p.pps) != 0 && (codec != videoHEVC || len(p.vps) != 0)
}

func (p videoParameters) prepend(codec byte, nalus [][]byte) [][]byte {
	out := [][]byte{p.sps, p.pps}
	if codec == videoHEVC {
		out = [][]byte{p.vps, p.sps, p.pps}
	}
	for _, nal := range nalus {
		kind := nalType(codec, nal)
		if codec == videoAVC && (kind == 7 || kind == 8) || codec == videoHEVC && kind >= 32 && kind <= 34 {
			continue
		}
		out = append(out, nal)
	}
	return out
}
