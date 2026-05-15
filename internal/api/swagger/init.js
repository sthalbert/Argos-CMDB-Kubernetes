// Bootstrap for the longue-vue Swagger UI shell. Kept as a separate file
// (rather than inline in index.html) so the strict CSP `script-src 'self'`
// on /docs/* responses accepts it without `'unsafe-inline'`.
window.ui = SwaggerUIBundle({
  url: '/openapi.yaml',
  dom_id: '#swagger-ui',
  presets: [SwaggerUIBundle.presets.apis, SwaggerUIStandalonePreset],
  layout: 'StandaloneLayout',
  deepLinking: true,
  persistAuthorization: true,
  withCredentials: true,
  responseInterceptor: function (res) {
    if (res.status === 401 && res.url && res.url.endsWith('/openapi.yaml')) {
      document.getElementById('swagger-ui').innerHTML =
        '<div class="lv-auth-prompt">' +
        '  <h2>Sign in to view the longue-vue API</h2>' +
        '  <p>This page requires authentication.</p>' +
        '  <a href="/ui/login">Sign in</a>' +
        '</div>';
    }
    return res;
  }
});
