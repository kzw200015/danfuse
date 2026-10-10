package testenv

import (
	"strings"
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/binding"
)

// XMLFile 一份 B 站导出的 XML 弹幕文件。ds 是 <d> 的 p 与正文交替排列。
func XMLFile(name string, ds ...string) binding.UploadedFile {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><i><chatserver>chat.bilibili.com</chatserver><chatid>934042</chatid>`)
	for i := 0; i < len(ds); i += 2 {
		b.WriteString(`<d p="` + ds[i] + `">` + ds[i+1] + "</d>\n")
	}
	b.WriteString("</i>")
	return binding.UploadedFile{Name: name, Data: []byte(b.String())}
}

// Snapshot1、Snapshot2 同一个视频不同日期的两份快照：dmid 2 两份都有（新快照里时间精度不同），dmid 1、3 各在一份里。
func Snapshot1(name string) binding.UploadedFile {
	return XMLFile(name,
		"1.5,1,25,16777215,1373250214,0,d9df08a7,1", "前排",
		"61.25,1,25,16777215,1373250228,0,500cea0d,2", "好看")
}

func Snapshot2(name string) binding.UploadedFile {
	return XMLFile(name,
		"61.2499980927,1,25,16777215,1373250228,0,500cea0d,2", "好看",
		"120,5,25,16711680,1373250271,0,3ae9ab45,3", "顶部")
}

// FileNames 绑定里的弹幕文件名，按加入的顺序。
func FileNames(t testing.TB, svc *binding.Service, id int64) []string {
	t.Helper()
	files, err := svc.ListFiles(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = f.Name
	}
	return names
}
