package agent

import (
	"os"
	"strconv"
	"strings"
)

func processGroupHasLiveMemberPlatform(pgid int) (bool, bool) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false, false
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		member, live := procStatProcessGroupMember(pid, pgid)
		if member && live {
			return true, true
		}
	}
	return false, true
}

func procStatProcessGroupMember(pid, pgid int) (bool, bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false, false
	}
	stat := string(data)
	endName := strings.LastIndex(stat, ")")
	if endName < 0 || endName+2 >= len(stat) {
		return false, false
	}
	fields := strings.Fields(stat[endName+2:])
	if len(fields) < 3 {
		return false, false
	}
	processGroup, err := strconv.Atoi(fields[2])
	if err != nil || processGroup != pgid {
		return false, false
	}
	return true, fields[0] != "Z"
}
