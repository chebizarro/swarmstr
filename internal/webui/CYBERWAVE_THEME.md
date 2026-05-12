# 🌌 Metiq Web UI - Cyberwave Theme

The Metiq web UI features a futuristic cyberwave aesthetic with electric purples, neon blues, and vibrant glows - creating an immersive AI agent interface that matches the terminal logging theme.

## Color Palette

The web UI uses the same cyberwave palette as the terminal logging:

```css
--accent: #B026FF;        /* Bright electric purple */
--accent-bright: #D580FF; /* Lighter neon purple */
--accent-dim: #7A1CAC;    /* Deep dark purple */
--info: #00D4FF;          /* Electric cyan */
--success: #39FF14;       /* Neon green */
--warn: #FFD700;          /* Electric gold */
--error: #FF006E;         /* Hot neon pink */
--text-muted: #9D9DAF;    /* Muted purple-gray */
```

## Visual Features

### Neon Glows
- **Status indicators** - Pulsing glow effects on connection status
- **Active elements** - Glowing borders and shadows on focused inputs
- **Buttons** - Gradient backgrounds with hover glow effects
- **Scrollbars** - Purple gradient with subtle glow

### Gradient Effects
- **Header title** - Electric purple to cyan gradient text
- **Send button** - Purple gradient with enhanced glow on hover
- **Background** - Subtle radial gradients for depth

### Interactive Elements
- **Hover states** - All interactive elements glow on hover
- **Active states** - Pressed buttons provide tactile feedback
- **Focus states** - Input fields glow when focused
- **Streaming indicator** - Animated cursor with purple accent

### Cyberpunk Touches
- **Message bubbles** - Subtle shadows with purple/cyan tints
- **Sidebar items** - Inset glow on active state
- **Thinking animation** - Bouncing dots with pulsing glow
- **Modal dialogs** - Strong shadow with purple accent border

## Component Breakdown

### Header
- Gradient text logo (purple → cyan)
- Glowing status dot (green for connected, pink for error)
- Purple-themed badges with glow

### Sidebar
- Purple border with subtle glow
- Active items have inset glow effect
- Tab underlines glow when active
- Channel indicators with neon green glow

### Messages
- User bubbles: Purple tint with soft shadow
- Agent bubbles: Cyan tint with soft shadow
- Error messages: Hot pink with text glow
- Streaming cursor: Electric purple animation

### Input Area
- Focus glow on textarea
- Gradient send button with hover lift effect
- Purple-themed placeholder text

### Modal/Approval
- Strong purple-accented border
- Glowing warning text
- Approve button: Neon green glow
- Deny button: Hot pink glow

## Comparison to Standard Theme

| Element | Standard | Cyberwave |
|---------|----------|-----------|
| Accent | `#6c63ff` | `#B026FF` (brighter purple) |
| Background | Dark gray | Deep black with gradient overlay |
| Borders | Muted gray | Purple-tinted borders |
| Effects | Minimal | Glows, gradients, shadows |
| Status dots | Flat | Glowing with box-shadow |
| Buttons | Flat hover | Gradient with lift effect |
| Scrollbar | Simple | Gradient with glow |

## Technical Details

### CSS Custom Properties
All colors are defined as CSS custom properties in `:root`, making the theme easily customizable.

### Transitions
Smooth transitions on:
- Color changes (0.15-0.2s)
- Border colors (0.15s)
- Box shadows (0.15-0.2s)
- Transforms (0.1s)

### Accessibility
- Color contrast maintained for text readability
- Focus states clearly visible with glow effects
- Interactive elements have clear hover/active states

### Performance
- CSS-only animations (no JavaScript overhead)
- Hardware-accelerated transforms
- Efficient box-shadow usage

## Design Philosophy

The cyberwave theme reflects Metiq's identity as an AI agent runtime:

- **Electric Purple** - Synthetic intelligence, neural networks
- **Neon Cyan** - Data streams, digital communication
- **Glowing Effects** - Energy, processing, active systems
- **Dark Background** - Focus on content, reduce eye strain
- **Gradients** - Depth, dimension, futuristic aesthetic

This isn't just a color scheme - it's a visual language that communicates "advanced AI infrastructure" at a glance.

## Browser Support

The theme uses modern CSS features:
- CSS Custom Properties (CSS Variables)
- Gradients (linear and radial)
- Box shadows (including multiple layers)
- Transforms and transitions
- Backdrop filters (for modal overlay)

Supported in all modern browsers (Chrome, Firefox, Safari, Edge).

## Customization

To adjust the theme intensity, modify the glow opacity values:

```css
/* Subtle glow */
--accent-glow: rgba(176, 38, 255, 0.2);

/* Strong glow */
--accent-glow: rgba(176, 38, 255, 0.5);
```

Or create a "low-power" mode by removing box-shadow effects while keeping the color palette.

---

⚡ **The future is electric. The future is cyberwave.** 💜
