package main

import "net/url"

func managementURL(addr, path, session string) string {
	base := "http://" + addr + path
	if session == "" {
		return base
	}

	values := url.Values{}
	values.Set("session", session)
	return base + "?" + values.Encode()
}
