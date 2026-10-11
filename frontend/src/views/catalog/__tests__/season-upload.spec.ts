import { describe, expect, it, vi } from 'vitest'

import { picked } from '@/__tests__/utils'

import { groupFolderFiles, hashEntries } from '../season-upload'

/** 分组结果里看得到的部分：文件夹名、条目名称与各自文件的相对路径（顺序与文件相同） */
function shape(paths: string[]) {
  const result = groupFolderFiles(paths.map(picked))
  if (!result.ok) {
    return result
  }
  return {
    ok: true,
    folder: result.folder,
    ignored: result.ignored,
    entries: result.entries.map((e) => ({
      label: e.label,
      paths: e.files.map((f) => f.webkitRelativePath),
    })),
  }
}

describe('groupFolderFiles', () => {
  it.each([
    [
      '一个子目录是一个条目，里面的 XML 合在一起',
      ['来自新世界/1/20120930.xml', '来自新世界/1/560685.xml', '来自新世界/2/20121007.xml'],
      [
        {
          label: '1',
          paths: ['来自新世界/1/560685.xml', '来自新世界/1/20120930.xml'],
        },
        { label: '2', paths: ['来自新世界/2/20121007.xml'] },
      ],
    ],
    [
      '顶层平铺的一份文件是一个条目，名称去掉扩展名',
      ['Legal High 第一季/1-「标题」.xml', 'Legal High 第一季/2-「标题」.XML'],
      [
        { label: '1-「标题」', paths: ['Legal High 第一季/1-「标题」.xml'] },
        { label: '2-「标题」', paths: ['Legal High 第一季/2-「标题」.XML'] },
      ],
    ],
    [
      '子目录与平铺文件混合，条目与文件按自然顺序排列，不看浏览器给出的顺序',
      ['番/10.xml', '番/2/b.xml', '番/1/20121007.xml', '番/2/a.xml', '番/1/20120930.xml'],
      [
        { label: '1', paths: ['番/1/20120930.xml', '番/1/20121007.xml'] },
        { label: '2', paths: ['番/2/a.xml', '番/2/b.xml'] },
        { label: '10', paths: ['番/10.xml'] },
      ],
    ],
  ])('%s', (_, paths, entries) => {
    expect(shape(paths)).toEqual({ ok: true, folder: paths[0]!.split('/')[0], ignored: 0, entries })
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
      folder: '来自新世界',
      ignored: 5,
      entries: [{ label: '1', paths: ['来自新世界/1/20120930.xml'] }],
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
    ['子目录和顶层文件同名', ['来自新世界/1/20120930.xml', '来自新世界/1.xml'], '条目名称重复：1'],
    ['顶层文件只差扩展名的大小写', ['来自新世界/1.xml', '来自新世界/1.XML'], '条目名称重复：1'],
  ])('%s', (_, paths, error) => {
    expect(shape(paths)).toEqual({ ok: false, error })
  })
})

const file = (content: string, name: string) => new File([content], name)

describe('hashEntries', () => {
  const entries = [
    { label: '1', files: [file('abc', 'a.xml'), file('', 'b.xml')] },
    { label: '2', files: [file('abc', 'c.xml')] },
  ]
  const want = [
    {
      label: '1',
      sha256s: [
        'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad',
        'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855',
      ],
    },
    {
      label: '2',
      sha256s: ['ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad'],
    },
  ]

  it('逐个条目算出各份文件内容的 SHA-256（小写十六进制），顺序与文件相同', async () => {
    expect(await hashEntries(entries)).toEqual(want)
  })

  // 用 http://局域网 IP 打开时不是安全上下文，浏览器不提供 crypto.subtle
  it('没有 crypto.subtle 时用纯 JS 算出同样的结果', async () => {
    vi.stubGlobal('crypto', {})
    try {
      expect(await hashEntries(entries)).toEqual(want)
      // 跨过填充边界（55、56、64 字节）与多字节字符的内容，对照 Python hashlib 的结果
      const more = [
        file('a'.repeat(55), '55.xml'),
        file('a'.repeat(56), '56.xml'),
        file('a'.repeat(64), '64.xml'),
        file('弹幕'.repeat(100), 'utf8.xml'),
      ]
      expect(await hashEntries([{ label: '长', files: more }])).toEqual([
        {
          label: '长',
          sha256s: [
            '9f4390f8d30c2dd92ec9f095b65e2b9ae9b0a925a5258e241c9f1e910f734318',
            'b35439a4ac6f0948b6d6f9e3c6af0f5f590ce20f1bde7090ef7970686ec6738a',
            'ffe054fe7ae0cb6dc65c3af9b61d5209f439851db43d0ba5997337df154668eb',
            'f7b18d93f89160383ce3317f4f1c2af0d930b9de0db05e98bc5316bbad5caa14',
          ],
        },
      ])
    } finally {
      vi.unstubAllGlobals()
    }
  })
})
