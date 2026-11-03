// End of the GitHub OAuth round trip (the API redirects here). Tells the studio tab, then closes
// when it was opened as a popup. The opener link may be gone after github.com, so a
// same-origin BroadcastChannel carries the result as well.
(function () {
  var status = new URLSearchParams(location.search).get('status');
  if (status !== 'ok' && status !== 'denied') status = 'error';
  var text = {
    ok: ['GitHub is connected', 'You can close this window and push your project from the Export dialog.'],
    denied: ['Access was not granted', 'Close this window and try again when you are ready.'],
    error: ['Connecting GitHub failed', 'The link expired or was opened in another browser. Close this window and try again.'],
  }[status];
  document.getElementById('title').textContent = text[0];
  document.getElementById('msg').textContent = text[1];
  var message = { type: 'sqc-github-oauth', status: status };
  try {
    var channel = new BroadcastChannel('sqc-github-oauth');
    channel.postMessage(message);
    channel.close();
  } catch (e) {}
  try {
    if (window.opener) window.opener.postMessage(message, location.origin);
  } catch (e) {}
  if (status === 'ok' && window.opener) setTimeout(function () { window.close(); }, 800);
})();
