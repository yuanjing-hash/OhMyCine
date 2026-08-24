package pttime

import "net/url"

func urlParseRelative(value string) (*url.URL, error) { return url.Parse(value) }
