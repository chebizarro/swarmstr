# 🌌 Theme Comparison: OpenClaw vs Metiq

## Visual Identity

### OpenClaw: Lobster Theme 🦞
- **Aesthetic**: Warm, approachable, coastal
- **Color Temperature**: Warm (reds, oranges)
- **Vibe**: Friendly, welcoming, organic

### Metiq: Cyberwave Theme ⚡
- **Aesthetic**: Futuristic, electric, cyberpunk
- **Color Temperature**: Cool (purples, blues)
- **Vibe**: High-tech, neon, synthetic intelligence

## Color Palette Comparison

| Purpose | OpenClaw (Lobster) | Metiq (Cyberwave) |
|---------|-------------------|-------------------|
| **Accent** | `#FF5A2D` 🔴 Coral red | `#B026FF` 💜 Electric purple |
| **Accent Bright** | `#FF7A3D` 🟠 Bright coral | `#D580FF` 💜 Neon purple |
| **Accent Dim** | `#D14A22` 🔴 Deep coral | `#7A1CAC` 💜 Dark purple |
| **Info** | `#FF8A5B` 🟠 Light coral | `#00D4FF` 💙 Electric cyan |
| **Success** | `#2FBF71` 🟢 Green | `#39FF14` 💚 Neon green |
| **Warn** | `#FFB020` 🟡 Orange | `#FFD700` 💛 Electric gold |
| **Error** | `#E23D2D` 🔴 Red | `#FF006E` 💗 Neon pink |
| **Muted** | `#8B7F77` ⚪ Warm gray | `#9D9DAF` ⚪ Purple-gray |

## Example Output Comparison

### OpenClaw Style
```
🦞 OpenClaw v1.0
Starting server...
✓ Database connected
⚠ Rate limit: 90%
✗ Connection failed
```
Colors: Warm oranges and reds

### Metiq Style
```
⚡ Metiq v1.0
Starting server...
✓ Database connected
⚠ Rate limit: 90%
✗ Connection failed
```
Colors: Cool purples and electric blues

## Design Philosophy

### OpenClaw
- **Inspiration**: Ocean, lobster traps, coastal tech
- **Feeling**: Approachable AI, friendly automation
- **User emotion**: Comfortable, trustworthy

### Metiq
- **Inspiration**: Cyberpunk, neon cities, synthetic intelligence
- **Feeling**: Advanced AI, cutting-edge runtime
- **User emotion**: Powerful, futuristic, electric

## When to Use Each

### Use OpenClaw Theme When:
- Building user-facing tools
- Want approachable, friendly vibe
- Preference for warm, organic aesthetic

### Use Metiq Theme When:
- Building AI agent infrastructure
- Want high-tech, futuristic vibe
- Preference for cool, electric aesthetic
- Running background daemons/services
- Emphasizing advanced AI capabilities

## Technical Implementation

Both themes use the same architecture:
1. **Palette** - Hex color definitions
2. **Theme** - Color function generators
3. **Logger** - Wrapped logging with colored output

OpenClaw uses `chalk` (Node.js), Metiq uses `fatih/color` (Go).

---

**Summary**: While OpenClaw's lobster theme is warm and welcoming, Metiq's cyberwave theme is electric and futuristic - perfect for an AI agent runtime that lives in the digital neon glow of modern infrastructure. ⚡💜
