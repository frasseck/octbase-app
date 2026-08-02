'use strict';
// Boots Swagger UI for docs.html. External file (not inline) so the Caddy CSP
// can stay script-src 'self'.
window.addEventListener('load', function () {
  window.ui = SwaggerUIBundle({
    url: '/openapi.yaml',
    dom_id: '#swagger-ui',
    deepLinking: true,
    displayRequestDuration: true,
    persistAuthorization: true,
    defaultModelsExpandDepth: -1,
  });
});
