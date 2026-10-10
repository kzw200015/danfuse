import { describe, expect, it } from 'vitest'

import { groupFolderFiles } from '../season-upload'

/** 目录选择选出的一份文件：jsdom 不实现 webkitRelativePath，手动补上 */
function picked(path: string) {
  const file = new File(['<i></i>'], path.split('/').at(-1)!, { type: 'text/xml' })
  Object.defineProperty(file, 'webkitRelativePath', { value: path })
  return file
}

/** 分组结果里看得到的部分：条目名称与各自的相对路径（文件与路径一一对应、顺序相同） */
function shape(paths: string[]) {
  const result = groupFolderFiles(paths.map(picked))
  if (!result.ok) {
    return result
  }
  for (const e of result.entries) {
    expect(e.files.map((f) => f.webkitRelativePath)).toEqual(e.paths)
  }
  return {
    ok: true,
    ignored: result.ignored,
    entries: result.entries.map((e) => ({ label: e.label, paths: e.paths })),
  }
}

describe('groupFolderFiles', () => {
  it.each([
    [
      '一个子目录是一个条目，里面的 XML 合在一起',
      ['来自新世界/1/20120930.xml', '来自新世界/1/560685.xml', '来自新世界/2/20121007.xml'],
      [
        {
          label: '来自新世界 / 1',
          paths: ['来自新世界/1/560685.xml', '来自新世界/1/20120930.xml'],
        },
        { label: '来自新世界 / 2', paths: ['来自新世界/2/20121007.xml'] },
      ],
    ],
    [
      '顶层平铺的一份文件是一个条目，名称去掉扩展名',
      ['Legal High 第一季/1-「标题」.xml', 'Legal High 第一季/2-「标题」.XML'],
      [
        { label: 'Legal High 第一季 / 1-「标题」', paths: ['Legal High 第一季/1-「标题」.xml'] },
        { label: 'Legal High 第一季 / 2-「标题」', paths: ['Legal High 第一季/2-「标题」.XML'] },
      ],
    ],
    [
      '子目录与平铺文件混合，条目与文件按自然顺序排列，不看浏览器给出的顺序',
      ['番/10.xml', '番/2/b.xml', '番/1/20121007.xml', '番/2/a.xml', '番/1/20120930.xml'],
      [
        { label: '番 / 1', paths: ['番/1/20120930.xml', '番/1/20121007.xml'] },
        { label: '番 / 2', paths: ['番/2/a.xml', '番/2/b.xml'] },
        { label: '番 / 10', paths: ['番/10.xml'] },
      ],
    ],
  ])('%s', (_, paths, entries) => {
    expect(shape(paths)).toEqual({ ok: true, ignored: 0, entries })
  })

  it('不是 .xml 的文件被忽略并计数，更深处的也一样', () => {
    expect(
      shape([
        '来自新世界/README.html',
        '来自新世界/.DS_Store',
        '来自新世界/1.zip',
        '来自新世界/1/20120930.xml',
        '来自新世界/1/comments.json',
        '来自新世界/1/old/backup.zip',
      ]),
    ).toEqual({
      ok: true,
      ignored: 5,
      entries: [{ label: '来自新世界 / 1', paths: ['来自新世界/1/20120930.xml'] }],
    })
  })

  it.each([
    [
      '超过两层，给出第一份越界文件的路径',
      ['10月/来自新世界/1/20120930.xml', '10月/来自新世界/2/20121007.xml'],
      '目录最多两层（文件夹 / 子目录 / 文件），这份文件超出了：10月/来自新世界/1/20120930.xml',
    ],
    [
      '一份 XML 都没有',
      ['神探夏洛克/1/danmaku.json', '神探夏洛克/README.html'],
      '所选文件夹里没有 XML 文件',
    ],
    [
      '子目录和顶层文件同名',
      ['来自新世界/1/20120930.xml', '来自新世界/1.xml'],
      '条目名称重复：来自新世界 / 1',
    ],
    [
      '顶层文件只差扩展名的大小写',
      ['来自新世界/1.xml', '来自新世界/1.XML'],
      '条目名称重复：来自新世界 / 1',
    ],
  ])('%s', (_, paths, error) => {
    expect(shape(paths)).toEqual({ ok: false, error })
  })
})
