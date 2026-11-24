package main

import "embed"

//go:embed web.html help.html
var webTemplateFiles embed.FS
