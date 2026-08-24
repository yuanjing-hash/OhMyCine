package pttime

import "mime"

func mimeParse(value string) (string, map[string]string, error) { return mime.ParseMediaType(value) }
