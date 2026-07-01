package monitor

import (
	"net"
	"sort"
	"strings"
)

func Lookup(domain string, recordType string) (string, *string) {
	domain = strings.TrimSpace(domain)
	recordType = strings.ToUpper(strings.TrimSpace(recordType))

	switch recordType {
	case "A":
		ips, err := net.LookupIP(domain)
		if err != nil {
			msg := err.Error()
			return "", &msg
		}
		var v4 []string
		for _, ip := range ips {
			if ip4 := ip.To4(); ip4 != nil {
				v4 = append(v4, ip4.String())
			}
		}
		sort.Strings(v4)
		return strings.Join(v4, ","), nil
	case "CNAME":
		cname, err := net.LookupCNAME(domain)
		if err != nil {
			msg := err.Error()
			return "", &msg
		}
		cname = strings.TrimSuffix(strings.TrimSpace(cname), ".")
		return cname, nil
	default:
		msg := "unsupported record type"
		return "", &msg
	}
}

