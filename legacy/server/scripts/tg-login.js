// One-time helper: creates a Telegram login session string (TG_SESSION) for MTProto backups.
//   npm install            (installs the "telegram" package)
//   TG_API_ID=123 TG_API_HASH=abc node scripts/tg-login.js
// API ID / hash: https://my.telegram.org -> API development tools.  Keep the printed session secret (it is a login to that account).
import readline from 'node:readline/promises';
import { TelegramClient } from 'telegram';
import { StringSession } from 'telegram/sessions/index.js';

const apiId = Number(process.env.TG_API_ID), apiHash = process.env.TG_API_HASH;
if (!apiId || !apiHash) { console.error('Set TG_API_ID and TG_API_HASH first.'); process.exit(1); }
const rl = readline.createInterface({ input: process.stdin, output: process.stdout });
const client = new TelegramClient(new StringSession(''), apiId, apiHash, { connectionRetries: 5 });
await client.start({
  phoneNumber: () => rl.question('Phone number (with country code): '),
  phoneCode: () => rl.question('Code Telegram sent you: '),
  password: () => rl.question('2-step password (leave empty if none): '),
  onError: (e) => console.error(e),
});
console.log('\nTG_SESSION=' + client.session.save() + '\n');
await client.disconnect(); rl.close(); process.exit(0);
