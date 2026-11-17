import { cp, readFile, rm, writeFile } from 'node:fs/promises'
import { dirname, relative, sep } from 'node:path'
import tailwind from '@tailwindcss/postcss'
import { build, context, type BuildOptions } from 'esbuild'
import postcss from 'postcss'

const dev = process.argv.includes('--dev')
const outdir = dev ? 'web/.dev' : 'web/dist'
const stylesheet = postcss([tailwind({ optimize: false })])
const options: BuildOptions = {
  entryPoints: ['main.tsx'],
  bundle: true,
  splitting: true,
  format: 'esm',
  target: 'es2022',
  minify: !dev,
  outdir,
  entryNames: 'assets/[name]-[hash]',
  chunkNames: 'assets/[name]-[hash]',
  assetNames: 'assets/[name]-[hash]',
  publicPath: '/',
  loader: { '.woff': 'file', '.woff2': 'file' },
  metafile: true,
  plugins: [{
    name: 'hoomail-client',
    setup(builder) {
      builder.onLoad({ filter: /[/\\]app[/\\]globals\.css$/ }, async ({ path }) => {
        const result = await stylesheet.process(await readFile(path, 'utf8'), { from: path })
        const watchFiles = ['index.html', 'public/hoomail-logo.png']
        const watchDirs = new Set<string>()
        for (const message of result.messages) {
          if (message.type === 'dependency') watchFiles.push(message.file)
          if (message.type === 'dir-dependency') watchDirs.add(message.dir)
        }
        return { contents: result.css, loader: 'css', resolveDir: dirname(path), watchFiles, watchDirs: [...watchDirs] }
      })
      builder.onEnd(async ({ metafile }) => {
        if (!metafile) return
        const entry = Object.entries(metafile.outputs).find(([, output]) => output.entryPoint === 'main.tsx')
        if (!entry?.[1].cssBundle) throw new Error('Expected a client script and stylesheet')
        const url = (path: string) => '/' + relative(outdir, path).split(sep).join('/')
        const template = await readFile('index.html', 'utf8')
        const script = '<script type="module" src="/main.tsx"></script>'
        if (!template.includes(script)) throw new Error('Missing client entry in index.html')
        await cp('public', outdir, { recursive: true })
        await writeFile(`${outdir}/index.html`, template
          .replace('</head>', `  <link rel="stylesheet" href="${url(entry[1].cssBundle)}" />\n  </head>`)
          .replace(script, `<script type="module" src="${url(entry[0])}"></script>`))
      })
    },
  }],
}

await rm(outdir, { recursive: true, force: true })
if (dev) {
  const client = await context(options)
  try {
    await client.rebuild()
    await client.watch()
    const { port } = await client.serve({ host: '127.0.0.1', port: 5173, servedir: outdir })
    console.log(`Frontend preview: http://127.0.0.1:${port}`)
  } catch (error) {
    await client.dispose()
    throw error
  }
} else {
  await build(options)
}
