export class HttpError extends Error {
  constructor(status, code) { super(code); this.status = status; this.code = code; }
}
export const deny = () => { throw new HttpError(403, 'permission_denied'); };
export const bad = (code = 'bad_request') => { throw new HttpError(400, code); };
