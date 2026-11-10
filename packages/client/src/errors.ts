export class APIError extends Error {readonly status:number;readonly code:string;constructor(status:number,code:string){super(code);this.status=status;this.code=code;this.name='APIError'}}
// Only these server codes are documented to be emitted before CreateRun.
export function isDefiniteMessageRejection(error:unknown):boolean{return error instanceof APIError&&((error.status===400&&error.code==='invalid_content')||(error.status===503&&error.code==='service_capacity'))}

// This error is constructed only before the admission POST has been invoked.
export class SessionAdmissionNotSentError extends Error {constructor(cause:unknown){super(`session admission not sent: ${cause instanceof Error?cause.message:String(cause)}`,{cause});this.name='SessionAdmissionNotSentError'}}
// Constructed only when the skill POST has not been invoked. Transport and
// response-validation failures after invocation stay unknown, never retryable.
export class SkillNotSentError extends Error {constructor(cause:unknown){super('skill request not sent',{cause});this.name='SkillNotSentError'}}
