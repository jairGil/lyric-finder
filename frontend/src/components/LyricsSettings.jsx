import React from 'react'

// La paleta del selector: los cinco acentos de la app y los dos neutros, que son
// los que se eligen con fondos claros y oscuros. Lo que viaja al backend es el
// índice, así que el orden importa y no se puede reordenar sin tocarlo allá.
const PALETTE = [
  { label: 'Azul', css: 'var(--accent-1)' },
  { label: 'Cian', css: 'var(--accent-2)' },
  { label: 'Violeta', css: 'var(--accent-3)' },
  { label: 'Rosa', css: 'var(--accent-4)' },
  { label: 'Naranja', css: 'var(--accent-5)' },
  { label: 'Blanco', css: '#ffffff' },
  // Negro puro no se distingue del panel, así que la muestra va un punto más
  // clara: lo que se dibuja en el video es el negro del backend igual.
  { label: 'Negro', css: '#151515' },
]

const HORIZONTAL_ALIGNMENTS = [
  { id: 'left', label: 'Izquierda' },
  { id: 'center', label: 'Centro' },
  { id: 'right', label: 'Derecha' },
]

const VERTICAL_ALIGNMENTS = [
  { id: 'top', label: 'Arriba' },
  { id: 'center', label: 'Centro' },
  { id: 'bottom', label: 'Abajo' },
]

const COLOR_MODES = [
  { id: 'single', label: 'Un solo color' },
  { id: 'multi', label: 'Varios colores' },
]

const COLOR_ORDERS = [
  { id: 'word', label: 'Por palabra' },
  { id: 'line', label: 'Por línea' },
]

// La muestra de cada fuente usa la misma clase que el reproductor, así que el Aa
// se ve igual que la letra que va a salir en pantalla.
const FONTS = [
  { id: 'manrope', label: 'Manrope' },
  { id: 'inter', label: 'Inter' },
  { id: 'mono', label: 'DM Mono' },
  { id: 'consolas', label: 'Consolas' },
  { id: 'serif', label: 'Source Serif' },
  { id: 'slab', label: 'Roboto Slab' },
  { id: 'cursive', label: 'Trebuchet' },
]

function Icon({ children }) {
  return (
    <svg className="style-icon" viewBox="0 0 24 24" width="20" height="20"
      fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" aria-hidden="true">
      {children}
    </svg>
  )
}

// El resaltado se explica con el dibujo: una barra que cubre toda la línea y otra
// que cubre solo una parte. El nombre va en el title y en el aria-label, que es lo
// que queda si la imagen no se ve.
function HighlightIcon({ id }) {
  const width = id === 'line' ? 18 : 9
  return (
    <Icon>
      <path d="M4 6h16" opacity=".3" />
      <rect x="3" y="9" width={width} height="6" rx="1.5" fill="currentColor" stroke="none" />
      <path d="M4 18h16" opacity=".3" />
    </Icon>
  )
}

function AlignIcon({ id }) {
  const top = { top: 5, center: 9, bottom: 13 }[id]
  return (
    <Icon>
      <rect x="3.5" y="3.5" width="17" height="17" rx="2" opacity=".35" />
      <rect x="7" y={top} width="10" height="3" rx="1" fill="currentColor" stroke="none" />
    </Icon>
  )
}

function HorizontalAlignIcon({ id }) {
  const paths = {
    left: 'M4 6h16M4 12h12M4 18h14',
    center: 'M4 6h16M6 12h12M5 18h14',
    right: 'M4 6h16M8 12h12M6 18h14',
  }
  return (
    <Icon>
      <path d={paths[id]} />
    </Icon>
  )
}

// Un solo color es un punto; varios son los mismos puntos repartidos, que es lo
// que se ve en el video.
function ColorModeIcon({ id }) {
  if (id === 'single') {
    return (
      <Icon>
        <circle cx="12" cy="12" r="6" fill="currentColor" stroke="none" />
      </Icon>
    )
  }
  return (
    <Icon>
      <circle cx="7" cy="8" r="3" fill="currentColor" stroke="none" />
      <circle cx="16" cy="7" r="3" fill="currentColor" stroke="none" opacity=".65" />
      <circle cx="11" cy="16" r="3" fill="currentColor" stroke="none" opacity=".85" />
    </Icon>
  )
}

function OrderIcon({ id }) {
  if (id === 'word') {
    return (
      <Icon>
        <rect x="3" y="7" width="7" height="4" rx="1" fill="currentColor" stroke="none" />
        <rect x="14" y="7" width="7" height="4" rx="1" fill="currentColor" stroke="none" opacity=".4" />
        <rect x="3" y="14" width="7" height="4" rx="1" fill="currentColor" stroke="none" opacity=".4" />
        <rect x="14" y="14" width="7" height="4" rx="1" fill="currentColor" stroke="none" opacity=".4" />
      </Icon>
    )
  }
  return (
    <Icon>
      <rect x="3" y="6" width="18" height="4" rx="1" fill="currentColor" stroke="none" />
      <rect x="3" y="14" width="18" height="4" rx="1" fill="currentColor" stroke="none" opacity=".4" />
    </Icon>
  )
}

export function LyricsSettings({ settings, onChange, onClose, expanded, inline = false, mode = 'export', onApply, pending = false }) {
  function update(field, value) {
    onChange({ ...settings, [field]: value })
  }

  const isPlayer = mode === 'player'
  const colors = settings.colors || 'single'
  const single = Number(settings.singleColor ?? 0)
  // Los colores marcados para varios colores. Sin lista guardada se toman los
  // tres primeros, que es un juego de contraste y lo que se ve de entrada.
  const chosen = Array.isArray(settings.palette) && settings.palette.length > 0
    ? settings.palette
    : [0, 1, 2]
  const order = COLOR_ORDERS.some((item) => item.id === settings.colorOrder) ? settings.colorOrder : 'word'
  // El tamaño es un porcentaje sobre el cuerpo que le toca al formato. Un valor
  // viejo o ausente es 100, que es el tamaño con el que siempre se exportó.
  const sizePercent = Math.min(160, Math.max(60, Number(settings.sizePercent) || 100))
  const legacyAlign = settings.align
  const horizontalAlign = HORIZONTAL_ALIGNMENTS.some((item) => item.id === settings.alignHorizontal)
    ? settings.alignHorizontal
    : ['left', 'right'].includes(legacyAlign) ? legacyAlign : 'center'
  const verticalAlign = VERTICAL_ALIGNMENTS.some((item) => item.id === settings.alignVertical)
    ? settings.alignVertical
    : VERTICAL_ALIGNMENTS.some((item) => item.id === legacyAlign) ? legacyAlign : 'center'
  const parsedLineCount = Number(settings.lines)
  const lineCount = Number.isFinite(parsedLineCount)
    ? Math.max(0, Math.min(5, Math.round(parsedLineCount)))
    : 2
  const horizontalLabelId = `settings-${mode}-horizontal-label`
  const verticalLabelId = `settings-${mode}-vertical-label`
  const linesLabelId = `settings-${mode}-lines-label`

  return (
    <div
      className={`settings-popover ${inline ? 'is-inline' : ''}`}
      role="dialog"
      aria-label="Configuración de letras"
    >
      <div className="settings-title">
        <span>Estilo de la letra</span>
        {onClose && (
          <button type="button" onClick={onClose} aria-label="Cerrar configuración">×</button>
        )}
      </div>

      {/* Los controles de alineación cambian por contexto; color y orden son comunes. */}
      <div className="style-row">
        <div className="settings-field">
          <span id={horizontalLabelId}>Horizontal</span>
          <div className="icon-group" role="radiogroup" aria-labelledby={horizontalLabelId}>
            {HORIZONTAL_ALIGNMENTS.map((option) => (
              <button
                key={option.id}
                type="button"
                className={`icon-option ${horizontalAlign === option.id ? 'is-selected' : ''}`}
                role="radio"
                aria-checked={horizontalAlign === option.id}
                aria-label={option.label}
                title={option.label}
                onClick={() => update('alignHorizontal', option.id)}
              >
                <HorizontalAlignIcon id={option.id} />
              </button>
            ))}
          </div>
        </div>

        {isPlayer ? (
          <div className="settings-field">
            <span id={linesLabelId}>Líneas arriba y abajo</span>
            <div className="line-count-control">
              <input
                type="number"
                min="0"
                max="5"
                step="1"
                value={lineCount}
                aria-labelledby={linesLabelId}
                onChange={(event) => {
                  const next = Number(event.target.value)
                  if (Number.isFinite(next)) update('lines', Math.max(0, Math.min(5, Math.round(next))))
                }}
              />
              <span>por lado</span>
            </div>
          </div>
        ) : (
          <div className="settings-field">
            <span id={verticalLabelId}>Vertical</span>
            <div className="icon-group" role="radiogroup" aria-labelledby={verticalLabelId}>
              {VERTICAL_ALIGNMENTS.map((option) => (
                <button
                  key={option.id}
                  type="button"
                  className={`icon-option ${verticalAlign === option.id ? 'is-selected' : ''}`}
                  role="radio"
                  aria-checked={verticalAlign === option.id}
                  aria-label={option.label}
                  title={option.label}
                  onClick={() => update('alignVertical', option.id)}
                >
                  <AlignIcon id={option.id} />
                </button>
              ))}
            </div>
          </div>
        )}

        <div className="settings-field">
          <span id="settings-colors-label">Colores</span>
          <div className="icon-group" role="radiogroup" aria-labelledby="settings-colors-label">
            {COLOR_MODES.map((option) => (
              <button
                key={option.id}
                type="button"
                className={`icon-option ${colors === option.id ? 'is-selected' : ''}`}
                role="radio"
                aria-checked={colors === option.id}
                aria-label={option.label}
                title={option.label}
                onClick={() => {
                  if (option.id === 'multi' && !settings.palette?.length) {
                    // Pasar a varios colores sin lista guardada arranca con los tres
                    // primeros, que es lo que muestra la paleta; si no, lo que se ve
                    // marcado no sería lo que se renderiza.
                    onChange({ ...settings, colors: 'multi', palette: [0, 1, 2] })
                    return
                  }
                  update('colors', option.id)
                }}
              >
                <ColorModeIcon id={option.id} />
              </button>
            ))}
          </div>
        </div>

        <div className="settings-field">
          <span id="settings-order-label">Orden del color</span>
          <div className="icon-group" role="radiogroup" aria-labelledby="settings-order-label">
            {COLOR_ORDERS.map((option) => (
              <button
                key={option.id}
                type="button"
                className={`icon-option ${order === option.id ? 'is-selected' : ''}`}
                role="radio"
                aria-checked={order === option.id}
                aria-label={option.label}
                title={option.label}
                onClick={() => update('colorOrder', option.id)}
              >
                <OrderIcon id={option.id} />
              </button>
            ))}
          </div>
        </div>
      </div>

      {/* La paleta ocupa todo el ancho. Con un solo color se elige uno; con varios
          se pueden marcar los que hagan falta, como un multiple select, y el render
          recorre esa lista en el orden en que se marcaron. */}
      <div className="settings-field">
        <span id="settings-palette-label">
          {colors === 'single' ? 'Color' : 'Colores'}
        </span>
        <div
          className={`color-grid ${colors === 'multi' ? 'is-multi' : ''}`}
          role={colors === 'single' ? 'radiogroup' : 'group'}
          aria-labelledby="settings-palette-label"
        >
          {PALETTE.map((swatch, index) => {
            const picked = colors === 'single' ? single === index : chosen.includes(index)
            return (
              <button
                key={swatch.label}
                type="button"
                className={`color-swatch ${picked ? 'is-selected' : ''}`}
                style={{ '--swatch': swatch.css }}
                role={colors === 'single' ? 'radio' : 'checkbox'}
                aria-checked={picked}
                aria-label={swatch.label}
                title={swatch.label}
                onClick={() => {
                  if (colors === 'single') {
                    update('singleColor', index)
                  } else {
                    // El orden de las marcas es el orden en que se recorren, así
                    // que se agrega al final y se saca sin correr el resto.
                    update('palette', picked ? chosen.filter((item) => item !== index) : [...chosen, index])
                  }
                }}
              />
            )
          })}
        </div>
      </div>

      {/* La fuente ocupa el ancho entero y adentro va la cuadrícula de cuadros,
          cada uno con su Aa: el nombre solo no dice cómo se ve la letra. */}
      <div className="settings-field">
        <span id="settings-font-label">Fuente</span>
        <div
          className="font-block"
          role="radiogroup"
          aria-labelledby="settings-font-label"
          style={{ '--size-ratio': sizePercent / 100 }}
        >
          {FONTS.map((option) => (
            <button
              key={option.id}
              type="button"
              className={`font-cell lyrics-font-${option.id} ${settings.font === option.id ? 'is-selected' : ''}`}
              role="radio"
              aria-checked={settings.font === option.id}
              title={option.label}
              onClick={() => update('font', option.id)}
            >
              <span className="font-sample" aria-hidden="true">Aa</span>
              <span className="font-name">{option.label}</span>
            </button>
          ))}
        </div>
      </div>

      {/* El tamaño de la letra va debajo de la fuente: son la misma decisión. El
          Aa de arriba crece con el control para que se vea el efecto sin esperar
          un render, y el backend lo aplica como porcentaje sobre el cuerpo que
          ya le correspondía al formato. */}
      <div className="settings-field">
        <span id="settings-size-label">Tamaño</span>
        <div className="font-size">
          <input
            type="range"
            min="60"
            max="160"
            step="10"
            value={sizePercent}
            aria-labelledby="settings-size-label"
            aria-valuetext={`${sizePercent}% del tamaño normal`}
            onChange={(event) => update('sizePercent', Number(event.target.value))}
          />
          <span className="font-size-value">{sizePercent}%</span>
        </div>
      </div>

      {/* El borrador se pide con este botón y no con cada cambio: un ffmpeg por
          cada color que se prueba es esperar de más por un video que se va a ver
          igual. */}
      {onApply && (
        <div className="settings-apply">
          <button type="button" className="settings-apply-button" onClick={onApply} disabled={!pending}>
            Aplicar
          </button>
          <span className={`settings-apply-state ${pending ? 'is-pending' : ''}`}>
            {pending ? 'sin aplicar' : 'aplicado'}
          </span>
        </div>
      )}
    </div>
  )
}
