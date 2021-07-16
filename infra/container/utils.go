package container

import (
	"crypto/sha1"
	"encoding/binary"
	"net"
	"strconv"
	"strings"

	v1 "github.com/alibaba/sealer/types/api/v1"
	"github.com/alibaba/sealer/utils"
)

func IsDockerAvailable() bool {
	lines, err := utils.RunSimpleCmd("docker -v")
	if err != nil || len(lines) != 1 {
		return false
	}
	return strings.Contains(lines, "docker version")
}

func GenerateSubnetFromName(name string, attempt int32) string {
	ip := make([]byte, 16)
	ip[0] = 0xfc
	ip[1] = 0x00
	h := sha1.New()
	_, _ = h.Write([]byte(name))
	_ = binary.Write(h, binary.LittleEndian, attempt)
	bs := h.Sum(nil)
	for i := 2; i < 8; i++ {
		ip[i] = bs[i]
	}
	subnet := &net.IPNet{
		IP:   net.IP(ip),
		Mask: net.CIDRMask(64, 128),
	}
	return subnet.String()
}

func getDiff(host v1.Hosts) (int, []string, error) {
	var num int
	var iplist []string
	count, err := strconv.Atoi(host.Count)
	if err != nil {
		return 0, nil, err
	}
	if count > len(host.IPList) {
		//scale up
		num = count - len(host.IPList)
	}

	if count < len(host.IPList) {
		//scale down
		iplist = host.IPList[count:]
	}

	return num, iplist, nil
}
