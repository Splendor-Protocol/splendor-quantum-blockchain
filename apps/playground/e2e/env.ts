// Own ports so a running `npm run playground` / `playground:api` is never reused by the suite.
export const WEB_PORT = 5181;
export const API_PORT = 8797;
export const FAKE_PORT = 8798;

export const E2E = {
  web: `http://127.0.0.1:${WEB_PORT}`,
  fake: `http://127.0.0.1:${FAKE_PORT}`,
  admin: `SQ:${'ad'.repeat(32)}`,
  user: `SQ:${'0e'.repeat(32)}`,
  recipient: `SQ:${'5a'.repeat(32)}`,
};
