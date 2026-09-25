// WebAuthn in the browser. The hub sends options with base64url strings where the
// API wants bytes, and gets base64url strings back.
import type { PasskeyAssertion, PasskeyAttestation } from './api'

/** Passkeys need a secure context: https, or http://localhost. */
export const passkeysSupported = () => window.isSecureContext && typeof window.PublicKeyCredential === 'function'

function toB64(buf: ArrayBuffer): string {
  let s = ''
  for (const b of new Uint8Array(buf)) s += String.fromCharCode(b)
  return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

function fromB64(s: string): Uint8Array<ArrayBuffer> {
  const b64 = s.replace(/-/g, '+').replace(/_/g, '/')
  const bin = atob(b64 + '='.repeat((4 - (b64.length % 4)) % 4))
  return Uint8Array.from(bin, (c) => c.charCodeAt(0))
}

type Descriptor = { type: 'public-key'; id: string }

/** Turns browser errors into sentences; cancelling is the common case. */
function explain(err: unknown): Error {
  if (err instanceof DOMException) {
    if (err.name === 'NotAllowedError') return new Error('The passkey request was cancelled or timed out.')
    if (err.name === 'InvalidStateError') return new Error('This passkey is already registered.')
    if (err.name === 'SecurityError') return new Error('Passkeys do not work with this address; open the hub by its hostname over https.')
  }
  return err instanceof Error ? err : new Error(String(err))
}

export async function getAssertion(opts: Record<string, unknown>): Promise<PasskeyAssertion> {
  let cred: Credential | null
  try {
    cred = await navigator.credentials.get({
      publicKey: {
        challenge: fromB64(opts.challenge as string),
        rpId: opts.rpId as string,
        timeout: opts.timeout as number,
        userVerification: 'required',
        allowCredentials: (opts.allowCredentials as Descriptor[] | undefined)?.map((c) => ({ type: c.type, id: fromB64(c.id) })),
      },
    })
  } catch (err) {
    throw explain(err)
  }
  if (!(cred instanceof PublicKeyCredential)) throw new Error('No passkey was selected.')
  const r = cred.response as AuthenticatorAssertionResponse
  return {
    id: toB64(cred.rawId),
    client_data: toB64(r.clientDataJSON),
    authenticator_data: toB64(r.authenticatorData),
    signature: toB64(r.signature),
    user_handle: r.userHandle ? toB64(r.userHandle) : '',
  }
}

export async function createPasskey(opts: Record<string, unknown>): Promise<PasskeyAttestation> {
  const user = opts.user as { id: string; name: string; displayName: string }
  let cred: Credential | null
  try {
    cred = await navigator.credentials.create({
      publicKey: {
        challenge: fromB64(opts.challenge as string),
        rp: opts.rp as PublicKeyCredentialRpEntity,
        user: { ...user, id: fromB64(user.id) },
        pubKeyCredParams: opts.pubKeyCredParams as PublicKeyCredentialParameters[],
        timeout: opts.timeout as number,
        excludeCredentials: (opts.excludeCredentials as Descriptor[]).map((c) => ({ type: c.type, id: fromB64(c.id) })),
        authenticatorSelection: opts.authenticatorSelection as AuthenticatorSelectionCriteria,
        attestation: 'none',
      },
    })
  } catch (err) {
    throw explain(err)
  }
  if (!(cred instanceof PublicKeyCredential)) throw new Error('No passkey was created.')
  const r = cred.response as AuthenticatorAttestationResponse
  const key = r.getPublicKey?.()
  if (!key) throw new Error('This browser does not share the passkey with the hub. Please use a current browser.')
  return {
    id: toB64(cred.rawId),
    client_data: toB64(r.clientDataJSON),
    authenticator_data: toB64(r.getAuthenticatorData()),
    public_key: toB64(key),
    algorithm: r.getPublicKeyAlgorithm(),
  }
}
