# Lyrics Finder

Frontend React + Vite para consumir la Lyrics API.

## Ejecutar

Desde esta carpeta:

```powershell
npm install
npm run dev
```

La API de Go debe estar ejecutándose en `http://localhost:8080`.

Cuando el backend está configurado con `YOUTUBE_API_KEY`, los resultados
incluyen un `youtubeVideoId` y el reproductor puede sincronizar la letra con
el tiempo real del video. Sin esa clave, se mantiene el reproductor karaoke
local y el enlace de búsqueda en YouTube.

Puedes cambiar la URL del backend con `VITE_API_URL`. Vite la incorpora en el
bundle durante el build, así que configúrala también en el proveedor de hosting
estático:

```powershell
$env:VITE_API_URL="http://localhost:8080"
npm run dev
```

Para publicar en Cloudflare Pages: build command `npm run build`, directorio de
salida `dist` y variable `VITE_API_URL` apuntando a la URL HTTPS pública del API.
El ejemplo local está en [`.env.example`](.env.example); cópialo a `.env.local`
para desarrollo.
