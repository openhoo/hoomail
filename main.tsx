import { render } from 'preact'
import { HoomailApp } from '@/components/hoomail/hoomail-app'
import '@/app/globals.css'

const root = document.getElementById('app')
if (!root) throw new Error('Missing #app root')
render(<HoomailApp />, root)
