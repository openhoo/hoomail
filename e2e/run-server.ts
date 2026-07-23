import { spawn, type ChildProcess } from 'node:child_process'
import { mkdir, rm } from 'node:fs/promises'
import process from 'node:process'

const runtimeDir = '.e2e-runtime'
const binaryPath = `${runtimeDir}/hoomail`
const httpPort = process.env.HOOMAIL_E2E_HTTP_PORT ?? '33100'
const smtpPort = process.env.HOOMAIL_E2E_SMTP_PORT ?? '33125'
const pop3Port = process.env.HOOMAIL_E2E_POP3_PORT ?? '33110'

type PromiseWithResolvers<T> = {
  promise: Promise<T>
  resolve: (value: T | PromiseLike<T>) => void
  reject: (reason?: unknown) => void
}

function run(command: string, args: string[]): Promise<void> {
  const { promise, resolve, reject } = (Promise as PromiseConstructor & {
    withResolvers<T>(): PromiseWithResolvers<T>
  }).withResolvers<void>()
  const child = spawn(command, args, { stdio: 'inherit' })
  child.once('error', reject)
  child.once('exit', (code, signal) => {
    if (code === 0) resolve()
    else reject(new Error(`${command} failed with ${signal ?? `exit code ${code ?? 1}`}`))
  })
  return promise
}


await rm(runtimeDir, { recursive: true, force: true })
await mkdir(runtimeDir)
await run('bun', ['run', 'build'])
await run('go', ['build', '-o', binaryPath, './cmd/hoomail'])

const server: ChildProcess = spawn(binaryPath, [], {
  stdio: 'inherit',
  env: {
    ...process.env,
    PORT: httpPort,
    HOOMAIL_SMTP_PORT: smtpPort,
    HOOMAIL_POP3_PORT: pop3Port,
    HOOMAIL_DB_PATH: `${runtimeDir}/hoomail.db`,
  },
})

let forwardedSignal: NodeJS.Signals | null = null
const forward = (signal: NodeJS.Signals) => {
  forwardedSignal = signal
  if (server.exitCode == null && server.signalCode == null) server.kill(signal)
}

process.once('SIGINT', () => forward('SIGINT'))
process.once('SIGTERM', () => forward('SIGTERM'))

server.once('error', (error) => {
  console.error(error)
  process.exitCode = 1
})

server.once('exit', (code, signal) => {
  if (code != null) process.exit(code)
  if (forwardedSignal != null && signal === forwardedSignal) process.exit(0)
  process.exit(1)
})
