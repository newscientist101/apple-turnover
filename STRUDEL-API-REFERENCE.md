# strudel API reference

strudel is a JavaScript live-coding language for writing music patterns, evaluated in the browser REPL.

This file is the function reference for an LLM agent operating the strudel REPL: every top-level
function, control parameter, pattern value and pattern method that pattern code can call, with its
signature and examples. Bare names are called directly (`s("bd")`); entries marked "Pattern method" are
chained onto a pattern (`"bd".fast(2)`); control parameters are set the same way (`"bd".lpf(500)`);
entries marked "a Pattern value" are used bare, without arguments (`sine.range(0,7)`).

Not covered here: mini-notation (the `"bd hh*2"` pattern language). That includes the `*` `/` `!` `?` `|`
`@` operators (repeat, slow, replicate, randomly drop, choose, elongate), the `~` and `-` rests, `,` for
parallel patterns, and the `.` / `:` shorthands: separators such as `s("bd:2")` set the sample index `n`,
not a speed. See the project's learn docs, website/src/pages/learn/mini-notation.mdx. Mini-notation has no
JSDoc entries, so it cannot appear below.

This file is also not exhaustive: about 40 further functions are exported but undocumented (they have no
JSDoc comment at all) by the REPL's packages - editor widgets, hydra, serial, mqtt and similar - so they
are absent here by construction.

Structure: `## @strudel/<package>` lists a package, `### <symbol>` documents one symbol; inside an entry,
`**Parameters**`, `**Returns**` and `**Examples**` are labelled blocks. `## Aliases` maps alternative names
to the symbol that documents them. Names in `Backticks` are symbols or code. Entries are alphabetical
within each package. A symbol that doc.json declares in both `Pattern#x` and `Pattern.x` scope appears once,
under its richest documentation; such collapsed spellings are listed as "Also documented as".

## @strudel/core

### add

`add()`

Pattern method — call it on a pattern: `pattern.add(...)`.

Assumes a pattern of numbers. Adds the given number to each item in the pattern.

**Examples**

```js
// Here, the triad 0, 2, 4 is shifted by different amounts
n("0 2 4".add("<0 3 4 0>")).scale("C:major")
// Without add, the equivalent would be:
// n("<[0 2 4] [3 5 7] [4 6 8] [0 2 4]>").scale("C:major")
```

```js
// You can also use add with notes:
note("c3 e3 g3".add("<0 5 7 0>"))
// Behind the scenes, the notes are converted to midi numbers:
// note("48 52 55".add("<0 5 7 0>"))
```

### adsr

`adsr(time: Number|Pattern, time: Number|Pattern, gain: Number|Pattern, time: Number|Pattern)`

ADSR envelope: Combination of Attack, Decay, Sustain, and Release.

**Parameters**

- `time` (`Number|Pattern`): attack time in seconds
- `time` (`Number|Pattern`): decay time in seconds
- `gain` (`Number|Pattern`): sustain level (0 to 1)
- `time` (`Number|Pattern`): release time in seconds

**Examples**

```js
note("[c3 bb2 f3 eb3]*2").sound("sawtooth").lpf(600).adsr(".1:.1:.5:.2")
```

### all

`all()`

Applies a function to all the running patterns. Note that the patterns are grouped together into a single `stack` before the function is applied. This is probably what you want, but see `each` for
a version that applies the function to each pattern separately.

**Note:** Patterns must be labeled (e.g. with `$:`) to be picked up by `all`. An unlabeled
pattern such as `note("c4")` is not registered and will produce no audio when `all` is present.
Use `$: note("c4")` instead.

```js
$: sound("bd - cp sd")
$: sound("hh*8")
all(fast("<2 3>"))
```

```js
$: sound("bd - cp sd")
$: sound("hh*8")
all(x => x.pianoroll())
```

### almostAlways

`almostAlways() -> Pattern`

Pattern method — call it on a pattern: `pattern.almostAlways(...)`.

Shorthand for `.sometimesBy(0.9, fn)`

**Examples**

```js
s("hh*8").almostAlways(x=>x.speed("0.5"))
```

### almostNever

`almostNever() -> Pattern`

Pattern method — call it on a pattern: `pattern.almostNever(...)`.

Shorthand for `.sometimesBy(0.1, fn)`

**Examples**

```js
s("hh*8").almostNever(x=>x.speed("0.5"))
```

### always

`always() -> Pattern`

Pattern method — call it on a pattern: `pattern.always(...)`.

Shorthand for `.sometimesBy(1, fn)` (always calls fn)

**Examples**

```js
s("hh*8").always(x=>x.speed("0.5"))
```

### anchor

`anchor(anchorNote: String|Pattern)`

The top note to align the voicing to. Defaults to c5

**Parameters**

- `anchorNote` (`String|Pattern`): the note to align the voicing or scale to

**Examples**

```js
anchor("<c4 g4 c5 g5>").chord("C").voicing()
```

```js
n("0 .. 7").anchor("<c4 g4 c5 g5>").scale("<C:major F:minor>")
```

### appBoth

`appBoth(pat_val: Pattern) -> Pattern`

Pattern method — call it on a pattern: `pattern.appBoth(...)`.

When this method is called on a pattern of functions, it matches its haps
with those in the given pattern of values.  A new pattern is returned, with
each matching value applied to the corresponding function.

In this `_appBoth` variant, where timespans of the function and value haps
are not the same but do intersect, the resulting hap has a timespan of the
intersection. This applies to both the part and the whole timespan.

**Parameters**

- `pat_val` (`Pattern`)

### appLeft

`appLeft(pat_val: Pattern) -> Pattern`

Pattern method — call it on a pattern: `pattern.appLeft(...)`.

As with `appBoth`, but the `whole` timespan is not the intersection,
but the timespan from the function of patterns that this method is called
on. In practice, this means that the pattern structure, including onsets,
are preserved from the pattern of functions (often referred to as the left
hand or inner pattern).

**Parameters**

- `pat_val` (`Pattern`)

### apply

`apply()`

Applies the given function to the pattern. Like layer, but with a single function:

**Examples**

```js
"<c3 eb3 g3>".scale('C minor').apply(scaleTranspose("0,2,4")).note()
```

### appRight

`appRight(pat_val: Pattern) -> Pattern`

Pattern method — call it on a pattern: `pattern.appRight(...)`.

As with `appLeft`, but `whole` timespans are instead taken from the
pattern of values, i.e. structure is preserved from the right hand/outer
pattern.

**Parameters**

- `pat_val` (`Pattern`)

### appWhole

`appWhole(whole_func: Function, func: Function) -> Pattern`

Pattern method — call it on a pattern: `pattern.appWhole(...)`.

Assumes 'this' is a pattern of functions, and given a function to
resolve wholes, applies a given pattern of values to that
pattern of functions.

**Parameters**

- `whole_func` (`Function`)
- `func` (`Function`)

### arp

`arp()`

Selects indices in in stacked notes.

**Examples**

```js
note("<[c,eb,g]!2 [c,f,ab] [d,f,ab]>")
.arp("0 [0,2] 1 [0,2]")
```

### arpWith

`arpWith()`

Selects indices in in stacked notes.

**Examples**

```js
note("<[c,eb,g]!2 [c,f,ab] [d,f,ab]>")
.arpWith(haps => haps[2])
```

### arrange

`arrange() -> Pattern`

Allows to arrange multiple patterns together over multiple cycles.
Takes a variable number of arrays with two elements specifying the number of cycles and the pattern to use.

**Examples**

```js
arrange(
  [4, "<c a f e>(3,8)"],
  [2, "<g a>(5,8)"]
).note()
```

### as

`as(mapping: String|Array)`

Sets properties in a batch.

**Parameters**

- `mapping` (`String|Array`): the control names that are set

**Examples**

```js
"c:.5 a:1 f:.25 e:.8".as("note:clip")
```

```js
"{0@2 0.25 0 0.5 .3 .5}%8".as("begin").s("sax_vib").clip(1)
```

### asNumber

`asNumber()`

Pattern method — call it on a pattern: `pattern.asNumber(...)`.

Returns a new pattern with all values parsed as numerals.

### asym

`asym(distortion: Number|Pattern, volume: Number|Pattern)`

Asymmetrical diode distortion

**Parameters**

- `distortion` (`Number|Pattern`): amount of distortion to apply
- `volume` (`Number|Pattern`): linear postgain of the distortion

### attack

`attack(attack: Number|Pattern)`

Synonyms: att

Amplitude envelope attack time: Specifies how long it takes for the sound to reach its peak value, relative to the onset.

**Parameters**

- `attack` (`Number|Pattern`): time in seconds.

**Examples**

```js
note("c3 e3 f3 g3").attack("<0 .1 .5>")
```

### bank

`bank(bank: String|Pattern)`

Select the sound bank to use. To be used together with `s`. The bank name (+ "_") will be prepended to the value of `s`.

**Parameters**

- `bank` (`String|Pattern`): the name of the bank

**Examples**

```js
s("bd sd [~ bd] sd").bank('RolandTR909') // = s("RolandTR909_bd RolandTR909_sd")
```

### base

`base(n: Number, b: Number, d: Number)`

Creates a pattern of numbers in base b from a number or pattern of numbers
limited to d digits long from the right

**Parameters**

- `n` (`Number`): number to convert (can be a pattern or array)
- `b` (`Number`): base to convert to (defaults to 10) (can be a pattern)
- `d` (`Number`): max number of digits to produce for each n (defaults to 0 for all) (can be a pattern)

**Examples**

```js
$: note(base("7175 543", 10, 3)).scale("c:major").s("saw")
// $: note("1 7 5 5 4 3").scale("c:major").s("saw")
```

### beat

`beat()`

creates a structure pattern from divisions of a cycle
especially useful for creating rhythms

**Examples**

```js
s("bd").beat("0,7,10", 16)
```

```js
s("sd").beat("4,12", 16)
```

### begin

`begin(amount: Number|Pattern)`

A pattern of numbers from 0 to 1. Skips the beginning of each sample, e.g. `0.25` to cut off the first quarter from each sample.

**Parameters**

- `amount` (`Number|Pattern`): between 0 and 1, where 1 is the length of the sample

**Examples**

```js
samples({ rave: 'rave/AREUREADY.wav' }, 'github:tidalcycles/dirt-samples')
s("rave").begin("<0 .25 .5 .75>").fast(2)
```

### berlin

`berlin` — a Pattern value, not a function. Use it directly, e.g. `berlin.range(...)`.

Generates a continuous pattern of [berlin noise](conceived by Jame Coyne and Jade Rowland as a joke but turned out to be surprisingly cool and useful,
like perlin noise but with sawtooth waves), in the range 0..1.

**Examples**

```js
// ascending arpeggios
n("0!16".add(berlin.fast(4).mul(14))).scale("d:minor")
```

### binary

`binary(n: Number)`

Creates a binary pattern from a number.

**Parameters**

- `n` (`Number`): input number to convert to binary

**Examples**

```js
"hh".s().struct(binary(5))
// "hh".s().struct("1 0 1")
```

### binaryL

`binaryL(n: Number)`

Creates a binary list pattern from a number.

**Parameters**

- `n` (`Number`): input number to convert to binary s("saw").seg(8).partials(binaryL(irand(4096).add(1)))

### binaryN

`binaryN(n: Number, nBits: Number)`

Creates a binary pattern from a number, padded to n bits long.

**Parameters**

- `n` (`Number`): input number to convert to binary
- `nBits` (`Number`): pattern length, defaults to 16

**Examples**

```js
"hh".s().struct(binaryN(55532, 16))
// "hh".s().struct("1 1 0 1 1 0 0 0 1 1 1 0 1 1 0 0")
```

### binaryNL

`binaryNL(n: Number, nBits: Number)`

Creates a binary list pattern from a number, padded to n bits long.

**Parameters**

- `n` (`Number`): input number to convert to binary
- `nBits` (`Number`): pattern length, defaults to 16

### bite

`bite(number: Number, slices: Number)`

Splits a pattern into the given number of slices, and plays them according to a pattern of slice numbers.
Similar to `slice`, but slices up patterns rather than sound samples.

**Parameters**

- `number` (`Number`): of slices
- `slices` (`Number`): to play

**Examples**

```js
note("0 1 2 3 4 5 6 7".scale('c:mixolydian'))
.bite(4, "3 2 1 0")
```

```js
sound("bd - bd bd*2, - sd:6 - sd:5 sd:1 - [- sd:2] -, hh [- cp:7]")
  .bank("RolandTR909").speed(1.2)
  .bite(4, "0 0 [1 2] <3 2> 0 0 [2 1] 3")
```

### bmod

`bmod(config: Object, config.bus?: String|Pattern, config.control?: String|Pattern, config.subControl?: String|Pattern, config.depth?: Number|Pattern, config.depthabs?: Number|Pattern, config.dc?: Number|Pattern, config.fxi?: Number|Pattern, id: String|Pattern) -> Pattern`

Modulates with the output from a given `bus`.
Can be called in sequence like pat.bmod(...).bmod(...) to set up multiple modulators

Send to an audio bus with `otherPat.bus(..)`.

There are two ways to declare which control will be modulated:

1. Explicitly put `control` in the config (e.g. `bmod({ id: 2, c: "lpf" })`)
2. If the control parameter is absent, the control *immediately before* the `bmod` call will be used
(e.g. `s("saw").lpf(500).bmod({ id: 2 })` to modulate `lpf`)

Modulators can be referred to by `id` so that they can be updated later e.g. inside
a `sometimes`. See example below.

**Parameters**

- `config` (`Object`): Bus modulation configuration.
- `config.bus?` (`String|Pattern`): Bus to get modulation signal from
- `config.control?` (`String|Pattern`): Node to modulate. Aliases: c
- `config.subControl?` (`String|Pattern`): Sub-control name to append to the control key. Aliases: sc
- `config.depth?` (`Number|Pattern`): Relative modulation depth. Aliases: dep, dr
- `config.depthabs?` (`Number|Pattern`): Absolute modulation depth. Aliases: da
- `config.dc?` (`Number|Pattern`): DC offset prior to application
- `config.fxi?` (`Number|Pattern`): FX index to target
- `id` (`String|Pattern`): ID to use for this modulator

**Examples**

```js
modulator: s("one").seg(64).gain(slider(0, 0, 1)).bus(1).dry(0)
carrier: s("saw").bmod({ b: 1 })
```

### bpattack

`bpattack(attack: Number|Pattern)`

Synonyms: bpa

Sets the attack duration for the bandpass filter envelope.

**Parameters**

- `attack` (`Number|Pattern`): time of the bandpass filter envelope

**Examples**

```js
note("c2 e2 f2 g2")
.sound('sawtooth')
.bpf(500)
.bpa("<.5 .25 .1 .01>/4")
.bpenv(4)
```

### bpdc

`bpdc(dcoffset: Number|Pattern)`

DC offset of the LFO for the bandpass filter

**Parameters**

- `dcoffset` (`Number|Pattern`): dc offset. set to 0 for unipolar

### bpdecay

`bpdecay(decay: Number|Pattern)`

Synonyms: bpd

Sets the decay duration for the bandpass filter envelope.

**Parameters**

- `decay` (`Number|Pattern`): time of the bandpass filter envelope

**Examples**

```js
note("c2 e2 f2 g2")
.sound('sawtooth')
.bpf(500)
.bpd("<.5 .25 .1 0>/4")
.bps(0.2)
.bpenv(4)
```

### bpdepth

`bpdepth(depth: Number|Pattern)`

Depth of the LFO for the bandpass filter

**Parameters**

- `depth` (`Number|Pattern`): depth of modulation

### bpdepthfrequency

`bpdepthfrequency(depth: Number|Pattern)`

Synonyms: bpdepthfreq

Depth of the LFO for the bandpass filter, in HZ

**Parameters**

- `depth` (`Number|Pattern`): depth of modulation

**Examples**

```js
note("<c c c# c c c4>*16").s("sawtooth").lpf(600).bpdepthfrequency("<200 500 100 0>")
```

### bpenv

`bpenv(modulation: Number|Pattern)`

Synonyms: bpe

Sets the bandpass filter envelope modulation depth.

**Parameters**

- `modulation` (`Number|Pattern`): depth of the bandpass filter envelope between 0 and *n*

**Examples**

```js
note("c2 e2 f2 g2")
.sound('sawtooth')
.bpf(500)
.bpa(.5)
.bpenv("<4 2 1 0 -1 -2 -4>/4")
```

### bpf

`bpf(frequency: Number|Pattern)`

Synonyms: bandf, bp

Sets the center frequency of the **b**and-**p**ass **f**ilter. When using mininotation, you
can also optionally supply the 'bpq' parameter separated by ':'.

**Parameters**

- `frequency` (`Number|Pattern`): center frequency

**Examples**

```js
s("bd sd [~ bd] sd,hh*6").bpf("<1000 2000 4000 8000>")
```

### bpq

`bpq(q: Number|Pattern)`

Synonyms: bandq

Sets the **b**and-**p**ass **q**-factor (resonance).

**Parameters**

- `q` (`Number|Pattern`): q factor

**Examples**

```js
s("bd sd [~ bd] sd").bpf(500).bpq("<0 1 2 3>")
```

### bprate

`bprate(rate: Number|Pattern)`

Rate of the LFO for the bandpass filter

**Parameters**

- `rate` (`Number|Pattern`): rate in hertz

### bprelease

`bprelease(release: Number|Pattern)`

Synonyms: bpr

Sets the release time for the bandpass filter envelope.

**Parameters**

- `release` (`Number|Pattern`): time of the bandpass filter envelope

**Examples**

```js
note("c2 e2 f2 g2")
.sound('sawtooth')
.clip(.5)
.bpf(500)
.bpenv(4)
.bpr("<.5 .25 .1 0>/4")
.release(.5)
```

### bpshape

`bpshape(shape: Number|Pattern)`

Shape of the LFO for the bandpass filter

**Parameters**

- `shape` (`Number|Pattern`): Shape of the lfo (0, 1, 2,..)

### bpskew

`bpskew(skew: Number|Pattern)`

Skew of the LFO for the bandpass filter

**Parameters**

- `skew` (`Number|Pattern`): How much to bend the LFO shape

### bpsustain

`bpsustain(sustain: Number|Pattern)`

Synonyms: bps

Sets the sustain amplitude for the bandpass filter envelope.

**Parameters**

- `sustain` (`Number|Pattern`): amplitude of the bandpass filter envelope

**Examples**

```js
note("c2 e2 f2 g2")
.sound('sawtooth')
.bpf(500)
.bpd(.5)
.bps("<0 .25 .5 1>/4")
.bpenv(4)
```

### bpsync

`bpsync(rate: Number|Pattern)`

Cycle-synced rate of the LFO for the bandpass filter

**Parameters**

- `rate` (`Number|Pattern`): rate in cycles

### brak

`brak() -> Pattern`

Returns a new pattern where every other cycle is played once, twice as
fast, and offset in time by one quarter of a cycle. Creates a kind of
breakbeat feel.

### brand

`brand` — a Pattern value, not a function. Use it directly, e.g. `brand.range(...)`.

A continuous pattern of 0 or 1 (binary random)

**Examples**

```js
s("hh*10").pan(brand)
```

### brandBy

`brandBy(probability: Number)`

A continuous pattern of 0 or 1 (binary random), with a probability for the value being 1

**Parameters**

- `probability` (`Number`): a number between 0 and 1

**Examples**

```js
s("hh*10").pan(brandBy(0.2))
```

### byteBeatExpression

`byteBeatExpression(byteBeatExpression: Number|Pattern)`

Synonyms: bbexpr, bb

Create byte beats with custom expressions

**Parameters**

- `byteBeatExpression` (`Number|Pattern`): bitwise expression for creating bytebeat

**Examples**

```js
s("bytebeat").bbexpr('t*(t>>15^t>>66)')
```

### byteBeatStartTime

`byteBeatStartTime(byteBeatStartTime: Number|Pattern)`

Synonyms: bbst

Create byte beats with custom expressions

**Parameters**

- `byteBeatStartTime` (`Number|Pattern`): in samples (t)

**Examples**

```js
note("c3!8".add("{0 0 12 0 7 5 3}%8")).s("bytebeat:5").bbst("<3 1>".mul(10000))._scope()
```

### cat

`cat(...items: any) -> Pattern`

Synonyms: slowcat

The given items are con**cat**enated, where each one takes one cycle.

**Parameters**

- `...items` (`any`): The items to concatenate

**Examples**

```js
cat("e5", "b4", ["d5", "c5"]).note()
// "<e5 b4 [d5 c5]>".note()
```

```js
// As a chained function:
s("hh*4").cat(
   note("c4(5,8)")
)
```

### ccn

`ccn(MIDI: Number|Pattern)`

MIDI control number: Sends a MIDI control change message.

**Parameters**

- `MIDI` (`Number|Pattern`): control number (0-127)

### ccv

`ccv(MIDI: Number|Pattern)`

MIDI control value: Sends a MIDI control change message.

**Parameters**

- `MIDI` (`Number|Pattern`): control value (0-127)

### ceil

`ceil() -> Pattern`

Pattern method — call it on a pattern: `pattern.ceil(...)`.

Assumes a numerical pattern. Returns a new pattern with all values set to
their mathematical ceiling. E.g. `3.2` replaced with `4`, and `-4.2`
replaced with `-4`.

**Examples**

```js
note("42 42.1 42.5 43".ceil())
```

### channel

`channel(channel: Number|Pattern)`

Choose the channel the pattern is sent to

**Parameters**

- `channel` (`Number|Pattern`): channel number

### channels

`channels(channels: Number|Pattern)`

Synonyms: ch

Allows you to set the output channels on the interface

**Parameters**

- `channels` (`Number|Pattern`): pattern the output channels

**Examples**

```js
note("e a d b g").channels("3:4")
```

### chebyshev

`chebyshev(distortion: Number|Pattern, volume: Number|Pattern)`

Distortion via Chebyshev polynomials

**Parameters**

- `distortion` (`Number|Pattern`): amount of distortion to apply
- `volume` (`Number|Pattern`): linear postgain of the distortion

### choose

`choose(...xs: any) -> Pattern`

Chooses randomly from the given list of elements.

Also documented as: `Pattern#choose` — Chooses from the given list of values (or patterns of values), according to the pattern that the method is called on. The pattern should be in the range 0.. 1.

**Parameters**

- `...xs` (`any`): values / patterns to choose from.

**Returns** `Pattern` — a continuous pattern.

**Examples**

```js
note("c2 g2!2 d2 f1").s(choose("sine", "triangle", "bd:6"))
```

### choose2

`choose2(...xs: any) -> Pattern`

Pattern method — call it on a pattern: `pattern.choose2(...)`.

As with choose, but the pattern that this method is called on should be
in the range -1 .. 1

**Parameters**

- `...xs` (`any`)

### chooseCycles

`chooseCycles() -> Pattern`

Synonyms: randcat

Picks one of the elements at random each cycle.

**Examples**

```js
chooseCycles("bd", "hh", "sd").s().fast(8)
```

```js
s("bd | hh | sd").fast(8)
```

### chooseInWith

`chooseInWith(pat: Pattern, xs: *) -> Pattern`

As with {chooseWith}, but the structure comes from the chosen values, rather
than the pattern you're using to choose with.

**Parameters**

- `pat` (`Pattern`)
- `xs` (`*`)

### chooseWith

`chooseWith(pat: Pattern, xs: *) -> Pattern`

Choose from the list of values (or patterns of values) using the given
pattern of numbers, which should be in the range of 0..1

**Parameters**

- `pat` (`Pattern`)
- `xs` (`*`)

**Examples**

```js
note("c2 g2!2 d2 f1").s(chooseWith(sine.fast(2), ["sawtooth", "triangle", "bd:6"]))
```

### chop

`chop() -> Pattern`

Pattern method — call it on a pattern: `pattern.chop(...)`.

Cuts each sample into the given number of parts, allowing you to explore a technique known as 'granular synthesis'.
It turns a pattern of samples into a pattern of parts of samples.

**Examples**

```js
samples({ rhodes: 'https://cdn.freesound.org/previews/132/132051_316502-lq.mp3' })
s("rhodes")
 .chop(4)
 .rev() // reverse order of chops
 .loopAt(2) // fit sample into 2 cycles
```

### chord

`chord(symbols: String|Pattern)`

The chord to voice

**Parameters**

- `symbols` (`String|Pattern`): chord symbols to voice e.g., C, Eb, Fm7, G7. The symbols can be defined via addVoicings

**Examples**

```js
chord("<Am C D F Am E Am E>").voicing()
```

### chorus

`chorus(chorus: String|Pattern)`

mix control for the chorus effect

**Parameters**

- `chorus` (`String|Pattern`): mix amount between 0 and 1

**Examples**

```js
note("d d a# a").s("sawtooth").chorus(.5)
```

### chunk

`chunk() -> Pattern`

Pattern method — call it on a pattern: `pattern.chunk(...)`.

Synonyms: slowChunk, slowchunk

Divides a pattern into a given number of parts, then cycles through those parts in turn, applying the given function to each part in turn (one part per cycle).

**Examples**

```js
"0 1 2 3".chunk(4, x=>x.add(7))
.scale("A:minor").note()
```

### chunkBack

`chunkBack() -> Pattern`

Pattern method — call it on a pattern: `pattern.chunkBack(...)`.

Synonyms: chunkback

Like `chunk`, but cycles through the parts in reverse order. Known as chunk' in tidalcycles

**Examples**

```js
"0 1 2 3".chunkBack(4, x=>x.add(7))
.scale("A:minor").note()
```

### chunkBackInto

`chunkBackInto()`

Pattern method — call it on a pattern: `pattern.chunkBackInto(...)`.

Synonyms: chunkbackinto

Like `chunkInto`, but moves backwards through the chunks.

**Examples**

```js
sound("bd sd ht lt bd - cp lt").chunkInto(4, hurry(2))
  .bank("tr909")
```

### chunkInto

`chunkInto()`

Pattern method — call it on a pattern: `pattern.chunkInto(...)`.

Synonyms: chunkinto

Like `chunk`, but the function is applied to a looped subcycle of the source pattern.

**Examples**

```js
sound("bd sd ht lt bd - cp lt").chunkInto(4, hurry(2))
  .bank("tr909")
```

### clearScope

`clearScope()`

Clears all user-defined variables and functions from the scope.
This removes variables created during block-based evaluation.

**Examples**

```js
// After defining variables in blocks:
// let myVar = 5
// function myFunc() { return 10; }
clearScope() // removes myVar and myFunc from scope
```

### clip

`clip(factor: Number|Pattern)`

Synonyms: legato

Multiplies the duration with the given number. Also cuts samples off at the end if they exceed the duration.

**Parameters**

- `factor` (`Number|Pattern`): = 0

**Examples**

```js
note("c a f e").s("piano").clip("<.5 1 2>")
```

### coarse

`coarse(factor: Number|Pattern)`

Fake-resampling for lowering the sample rate. Caution: This effect seems to only work in chromium based browsers

**Parameters**

- `factor` (`Number|Pattern`): 1 for original 2 for half, 3 for a third and so on.

**Examples**

```js
s("bd sd [~ bd] sd,hh*8").coarse("<1 4 8 16 32>")
```

### color

`color(color: String)`

Synonyms: colour

Sets the color of the hap in visualizations like pianoroll or highlighting.

**Parameters**

- `color` (`String`): Hexadecimal or CSS color name

### compress

`compress()`

Compress each cycle into the given timespan, leaving a gap

**Examples**

```js
cat(
  s("bd sd").compress(.25,.75),
  s("~ bd sd ~")
)
```

### compressor

`compressor(...)`

Requires arguments: at least 2.

Dynamics Compressor. The params are `compressor("threshold:ratio:knee:attack:release")`
More info here (https://developer.mozilla.org/en-US/docs/Web/API/DynamicsCompressorNode?retiredLocale=de#instance_properties)

**Examples**

```js
s("bd sd [~ bd] sd,hh*8")
.compressor("-20:20:10:.002:.02")
```

### contract

`contract()`

*Experimental*

Contracts the step size of the pattern by the given factor. See also `expand`.

**Examples**

```js
sound("tha dhi thom nam").bank("mridangam").contract("3 2 1 1 2 3").pace(8)
```

### control

`control(MIDI: Number|Pattern, MIDI: Number|Pattern)`

MIDI control: Sends a MIDI control change message.

**Parameters**

- `MIDI` (`Number|Pattern`): control number (0-127)
- `MIDI` (`Number|Pattern`): controller value (0-127)

### cosine

`cosine` — a Pattern value, not a function. Use it directly, e.g. `cosine.range(...)`.

A cosine signal between 0 and 1.

**Examples**

```js
n(stack(sine,cosine).segment(16).range(0,15))
.scale("C:minor")
```

### cosine2

`cosine2` — a Pattern value, not a function. Use it directly, e.g. `cosine2.range(...)`.

A cosine signal between -1 and 1 (like `cosine`, but bipolar).

### cpm

`cpm()`

**Deprecated**: still available in the REPL, but prefer the current alternative.

Plays the pattern at the given cycles per minute.

**Examples**

```js
s("<bd sd>,hh*2").cpm(90) // = 90 bpm
```

### crush

`crush(depth: Number|Pattern)`

Bit crusher effect.

**Parameters**

- `depth` (`Number|Pattern`): between 1 (for drastic reduction in bit-depth) to 16 (for barely no reduction).

**Examples**

```js
s("<bd sd>,hh*3").fast(2).crush("<16 8 7 6 5 4 3 2>")
```

### cubic

`cubic(distortion: Number|Pattern, volume: Number|Pattern)`

Cubic polynomial distortion

**Parameters**

- `distortion` (`Number|Pattern`): amount of distortion to apply
- `volume` (`Number|Pattern`): linear postgain of the distortion

### cut

`cut(group: Number|Pattern)`

In the style of classic drum-machines, `cut` will stop a playing sample as soon as another samples with in same cutgroup is to be played. An example would be an open hi-hat followed by a closed one, essentially muting the open.

**Parameters**

- `group` (`Number|Pattern`): cut group number

**Examples**

```js
s("[oh hh]*4").cut(1)
```

### cyclesPer

`cyclesPer` — a Pattern value, not a function. Use it directly, e.g. `cyclesPer.range(...)`.

A pattern measuring the duration of events,
in cycles per event. `cyclesPer` doesn't have structure itself, but takes structure, and therefore
event durations, from the pattern that it is combined with.
For example `cyclesPer.struct("1 1 [1 1] 1")` would give the same as `"0.25 0.25 [0.125 0.125] 0.25"`.
See also its reciprocal, `per`, also known as `perCycle`.

**Examples**

```js
// Shorter events are lower in pitch
sound("saw saw [saw saw] saw")
  .note(cyclesPer.range(50, 100))
```

```js
sound("bd sd [bd bd] sd*4 [- sd] [bd [bd bd]]")
  .note(cyclesPer.add(20))
```

### decay

`decay(time: Number|Pattern)`

Synonyms: dec

Amplitude envelope decay time: the time it takes after the attack time to reach the sustain level.
Note that the decay is only audible if the sustain value is lower than 1.

**Parameters**

- `time` (`Number|Pattern`): decay time in seconds

**Examples**

```js
note("c3 e3 f3 g3").decay("<.1 .2 .3 .4>").sustain(0)
```

### defragmentHaps

`defragmentHaps()`

Pattern method — call it on a pattern: `pattern.defragmentHaps(...)`.

Combines adjacent haps with the same value and whole.  Only
intended for use in tests.

### degrade

`degrade() -> Pattern`

Pattern method — call it on a pattern: `pattern.degrade(...)`.

Randomly removes 50% of events from the pattern. Shorthand for `.degradeBy(0.5)`

**Examples**

```js
s("hh*8").degrade()
```

```js
s("[hh?]*8")
```

### degradeBy

`degradeBy(amount: Number) -> Pattern`

Pattern method — call it on a pattern: `pattern.degradeBy(...)`.

Randomly removes events from the pattern by a given amount.
0 = 0% chance of removal
1 = 100% chance of removal

**Parameters**

- `amount` (`Number`): a number between 0 and 1

**Examples**

```js
s("hh*8").degradeBy(0.2)
```

```js
s("[hh?0.2]*8")
```

```js
//beat generator
s("bd").segment(16).degradeBy(.5).ribbon(16,1)
```

### delay

`delay(level: Number|Pattern)`

Sets the level of the delay signal.

When using mininotation, you can also optionally add the 'delaytime' and 'delayfeedback' parameter,
separated by ':'.

**Parameters**

- `level` (`Number|Pattern`): between 0 and 1

**Examples**

```js
s("bd bd").delay("<0 .25 .5 1>")
```

```js
s("bd bd").delay("0.65:0.25:0.9 0.65:0.125:0.7")
```

### delayfeedback

`delayfeedback(feedback: Number|Pattern)`

Synonyms: delayfb, dfb

Sets the level of the signal that is fed back into the delay.
Caution: Values >= 1 will result in a signal that gets louder and louder! Don't do it

**Parameters**

- `feedback` (`Number|Pattern`): between 0 and 1

**Examples**

```js
s("bd").delay(.25).delayfeedback("<.25 .5 .75 1>")
```

### delaysync

`delaysync(cycles: Number|Pattern)`

Synonyms: delays, ds

Sets the time of the delay effect in cycles.

**Parameters**

- `cycles` (`Number|Pattern`): delay length in cycles

**Examples**

```js
s("bd bd").delay(.25).delaysync("<1 2 3 5>".div(8))
```

### delaytime

`delaytime(delay: Number|Pattern)`

Synonyms: delayt, dt

Sets the time of the delay effect in seconds.

**Parameters**

- `delay` (`Number|Pattern`): in seconds

**Examples**

```js
note("d d a# a".fast(2))
.s("sawtooth")
.delay(.8)
.delaytime(1/2)
.delayspeed("<2 .5 -1 -2>")
```

### density

`density(density: Number|Pattern)`

crackle noise density

**Parameters**

- `density` (`Number|Pattern`): between 0 and x

**Examples**

```js
s("crackle*4").density("<0.01 0.04 0.2 0.5>".slow(4))
```

### detune

`detune(amount: Number|Pattern)`

Synonyms: det

Set detune for stacked voices of supported oscillators.

**Parameters**

- `amount` (`Number|Pattern`)

**Examples**

```js
note("d f a a# a d3").fast(2).s("supersaw").detune("<.1 .2 .5 24.1>")
```

### dictionary

`dictionary(dictionaryName: String)`

Which dictionary to use for the voicings. This falls back to the default dictionary if not provided

**Parameters**

- `dictionaryName` (`String`): which dictionary (having been defined with `addVoicings`) to use

**Examples**

```js
addVoicings('house', {
'': ['7 12 16', '0 7 16', '4 7 12'],
'm': ['0 3 7']
})
chord("<Am C D F Am E Am E>")
.dict('house').anchor(66)
.voicing().room(.5)
```

### diode

`diode(distortion: Number|Pattern, volume: Number|Pattern)`

Diode-emulating distortion

**Parameters**

- `distortion` (`Number|Pattern`): amount of distortion to apply
- `volume` (`Number|Pattern`): linear postgain of the distortion

### discreteOnly

`discreteOnly() -> Pattern`

Pattern method — call it on a pattern: `pattern.discreteOnly(...)`.

Returns a new pattern, with 'continuous' haps (those without 'whole'
timespans) removed from query results.

### distort

`distort(distortion: Number|Pattern, volume: Number|Pattern, type: Number|String|Pattern)`

Synonyms: dist

Wave shaping distortion. CAUTION: it can get loud.
Second option in optional array syntax (ex: ".9:.5") applies a postgain to the output. Third option sets the waveshaping type.
Most useful values are usually between 0 and 10 (depending on source gain). If you are feeling adventurous, you can turn it up to 11 and beyond ;)

**Parameters**

- `distortion` (`Number|Pattern`): amount of distortion to apply
- `volume` (`Number|Pattern`): linear postgain of the distortion
- `type` (`Number|String|Pattern`): type of distortion to apply

**Examples**

```js
s("bd sd [~ bd] sd,hh*8").distort("<0 2 3 10:.5>")
```

```js
note("d1!8").s("sine").penv(36).pdecay(.12).decay(.23).distort("8:.4")
```

```js
s("bd:4*4").bank("tr808").distort("3:0.5:diode")
```

### distorttype

`distorttype(type: Number|String|Pattern)`

Synonyms: disttype

Type of waveshaping distortion to apply.

**Parameters**

- `type` (`Number|String|Pattern`): type of distortion to apply

**Examples**

```js
s("bd*4").bank("tr909").distort(2).distorttype("<0 1 2>")
```

```js
s("sine").note("F1*2").release(1)
  .penv(24).pdecay(0.05)
  .distort(rand.range(1, 8))
  .distorttype("<fold chebyshev scurve diode asym sinefold>")
```

### distortvol

`distortvol(volume: Number|Pattern)`

Synonyms: distortion, distvol

Postgain for waveshaping distortion.

**Parameters**

- `volume` (`Number|Pattern`): linear postgain of the distortion

**Examples**

```js
s("bd*4").bank("tr909").distort(2).distortvol(0.8)
```

### div

`div()`

Pattern method — call it on a pattern: `pattern.div(...)`.

Divides each number by the given factor.

### djf

`djf(cutoff: Number|Pattern)`

DJ filter, below 0.5 is low pass filter, above is high pass filter.

**Parameters**

- `cutoff` (`Number|Pattern`): below 0.5 is low pass filter, above is high pass filter

**Examples**

```js
n(irand(16).seg(8)).scale("d:phrygian").s("supersaw").djf("<.5 .3 .2 .75>")
```

### drawLine

`drawLine(pattern: Pattern, chars: Number) -> String`

Intended for a debugging, drawLine renders the pattern as a string, where each character represents the same time span.
Should only be used with single characters as values, otherwise the character slots will be messed up.
Character legend:

- "|" cycle separator
- "-" hold previous value
- "." silence

**Parameters**

- `pattern` (`Pattern`): the pattern to use
- `chars` (`Number`): max number of characters (approximately)

**Examples**

```js
const line = drawLine("0 [1 2 3]", 10); // |0--123|0--123
console.log(line);
silence;
```

### drive

`drive(amount: Number|Pattern)`

Filter overdrive for supported filter types

**Parameters**

- `amount` (`Number|Pattern`)

**Examples**

```js
note("{f g g c d a a#}%16".sub(17)).s("supersaw").lpenv(8).lpf(150).lpq(.8).ftype('ladder').drive("<.5 4>")
```

### drop

`drop() -> Pattern`

*Experimental*

Drops the given number of steps from a pattern.
A positive number will drop steps from the start of a pattern, and a negative number from the end.

**Examples**

```js
"tha dhi thom nam".drop("1").sound().bank("mridangam")
```

```js
"tha dhi thom nam".drop("-1").sound().bank("mridangam")
```

```js
"tha dhi thom nam".drop("0 1 2 3").sound().bank("mridangam")
```

```js
"tha dhi thom nam".drop("0 -1 -2 -3").sound().bank("mridangam")
```

### duckattack

`duckattack(time: Number|Pattern)`

Synonyms: duckatt, datt

The time required for the ducked signal(s) to return to their normal volume.

Can vary across orbits with the ':' mininotation, e.g. `duckonset("0:0.003")`.
Note: this requires first applying the effect to multiple orbits with e.g. `duckorbit("2:3")`.

**Parameters**

- `time` (`Number|Pattern`): The attack time in seconds

**Examples**

```js
sound: n(run(8)).scale("c:minor").s("sawtooth").delay(.7).orbit(2)
ducker: s("bd:4!4").beat("0,4,8,11,14",16).duckorbit(2).duckattack("<0.2 0 0.4>").duckdepth(1)
```

```js
moreduck: n(run(8)).scale("c:minor").s("sawtooth").delay(.7).orbit(2)
lessduck: s("hh*16").orbit(5)
ducker: s("bd:4!4").beat("0,4,8,11,14",16).duckorbit("2:5").duckattack("0.4:0.1")
```

### duckdepth

`duckdepth(depth: Number|Pattern)`

The amount of ducking applied to target orbit

Can vary across orbits with the ':' mininotation, e.g. `duckdepth("0.3:0.1")`.
Note: this requires first applying the effect to multiple orbits with e.g. `duckorbit("2:3")`.

**Parameters**

- `depth` (`Number|Pattern`): depth of modulation from 0 to 1

**Examples**

```js
stack( n(run(8)).scale("c:minor").s("sawtooth").delay(.7).orbit(2), s("bd:4!4").beat("0,4,8,11,14",16).duckorbit(2).duckattack(0.2).duckdepth("<1 .9 .6 0>"))
```

```js
$: n(run(16)).scale("c:minor:pentatonic").s("sawtooth").delay(.7).orbit(2)
$: s("hh*16").orbit(3)
$: s("bd:4!4").beat("0,4,8,11,14",16).duckorbit("2:3").duckattack(0.2).duckdepth("1:0.5")
```

### duckonset

`duckonset(time: Number|Pattern)`

Synonyms: duckons

The time required for the ducked signal(s) to reach their lowest volume.
Can be used to prevent clicking or for creative rhythmic effects.

Can vary across orbits with the ':' mininotation, e.g. `duckonset("0:0.003")`.
Note: this requires first applying the effect to multiple orbits with e.g. `duckorbit("2:3")`.

**Parameters**

- `time` (`Number|Pattern`): The onset time in seconds

**Examples**

```js
// Clicks
sound: freq("63.2388").s("sine").orbit(2).gain(4)
duckerWithClick: s("bd*4").duckorbit(2).duckattack(0.3).duckonset(0).postgain(0)
```

```js
// No clicks
sound: freq("63.2388").s("sine").orbit(2).gain(4)
duckerWithoutClick: s("bd*4").duckorbit(2).duckattack(0.3).duckonset(0.01).postgain(0)
```

```js
// Rhythmic
noise: s("pink").distort("2:1").orbit(4) // used rhythmically with 0.3 onset below
hhat: s("hh*16").orbit(7)
ducker: s("bd*4").bank("tr909").duckorbit("4:7").duckonset("0.3:0.003").duckattack(0.25)
```

### duckorbit

`duckorbit(orbit: Number|Pattern)`

Synonyms: duck

Modulate the amplitude of an orbit to create a "sidechain" like effect.

Can be applied to multiple orbits with the ':' mininotation, e.g. `duckorbit("2:3")`

**Parameters**

- `orbit` (`Number|Pattern`): target orbit

**Examples**

```js
$: n(run(16)).scale("c:minor:pentatonic").s("sawtooth").delay(.7).orbit(2)
$: s("bd:4!4").beat("0,4,8,11,14",16).duckorbit(2).duckattack(0.2).duckdepth(1)
```

```js
$: n(run(16)).scale("c:minor:pentatonic").s("sawtooth").delay(.7).orbit(2)
$: s("hh*16").orbit(3)
$: s("bd:4!4").beat("0,4,8,11,14",16).duckorbit("2:3").duckattack(0.2).duckdepth(1)
```

### duration

`duration(seconds: Number|Pattern)`

Synonyms: dur

Sets the duration of the event in cycles. Similar to clip / legato, it also cuts samples off at the end if they exceed the duration.

**Parameters**

- `seconds` (`Number|Pattern`): = 0

**Examples**

```js
note("c a f e").s("piano").dur("<.5 1 2>")
```

### each

`each()`

Applies a function to each of the running patterns separately. This is intended for future use with upcoming 'stepwise' features. See `all` for a version that applies the function to all the patterns stacked together into a single pattern.

**Note:** Patterns must be labeled (e.g. with `$:`) to be picked up by `each`. An unlabeled
pattern such as `note("c4")` is not registered and will produce no audio when `each` is present.
Use `$: note("c4")` instead.

```js
$: sound("bd - cp sd")
$: sound("hh*8")
each(fast("<2 3>"))
```

### early

`early(cycles: Number|Pattern) -> Pattern`

Pattern method — call it on a pattern: `pattern.early(...)`.

Nudge a pattern to start earlier in time. Equivalent of Tidal's <~ operator

**Parameters**

- `cycles` (`Number|Pattern`): number of cycles to nudge left

**Examples**

```js
"bd ~".stack("hh ~".early(.1)).s()
```

### echo

`echo(times: Number, time: Number, feedback: Number) -> Pattern`

Pattern method — call it on a pattern: `pattern.echo(...)`.

Superimpose and offset multiple times, gradually decreasing the velocity

**Parameters**

- `times` (`Number`): how many times to repeat
- `time` (`Number`): cycle offset between iterations
- `feedback` (`Number`): velocity multiplicator for each iteration

**Examples**

```js
s("bd sd").echo(3, 1/6, .8)
```

### echoWith

`echoWith(times: Number, time: Number, func: Function)`

Synonyms: echowith, stutWith, stutwith

Superimpose and offset multiple times, applying the given function each time.

**Parameters**

- `times` (`Number`): how many times to repeat
- `time` (`Number`): cycle offset between iterations
- `func` (`Function`): function to apply, given the pattern and the iteration index

**Examples**

```js
"<0 [2 4]>"
.echoWith(4, 1/8, (p,n) => p.add(n*2))
.scale("C:minor").note()
```

### end

`end(length: Number|Pattern)`

Pattern method — call it on a pattern: `pattern.end(...)`.

The same as .begin, but cuts off the end off each sample.

**Parameters**

- `length` (`Number|Pattern`): 1 = whole sample,.5 = half sample,.25 = quarter sample etc..

**Examples**

```js
s("bd*2,oh*4").end("<.1 .2 .5 1>").fast(2)
```

### env

`env(config: Object, config.control?: String|Pattern, config.subControl?: String|Pattern, config.depth?: Number|Pattern, config.depthabs?: Number|Pattern, config.attack?: Number|Pattern, config.decay?: Number|Pattern, config.sustain?: Number|Pattern, config.release?: Number|Pattern, config.acurve?: Number|Pattern, config.dcurve?: Number|Pattern, config.rcurve?: Number|Pattern, config.fxi?: Number|Pattern, id: String|Pattern) -> Pattern`

Configures an envelope. Can be called in sequence like pat.env(...).env(...) to set up multiple envelopes
There are two ways to declare which control will be modulated:

1. Explicitly put `control` in the config (e.g. `env({ c: "lpf" })`)
2. If the control parameter is absent, the control *immediately before* the `env` call will be used
(e.g. `s("saw").lpf(500).env({ a: 1 })` to modulate `lpf`)

Modulators can be referred to by `id` so that they can be updated later e.g. inside
a `sometimes`. See example below.

**Parameters**

- `config` (`Object`): Envelope configuration.
- `config.control?` (`String|Pattern`): Node to modulate. Aliases: c
- `config.subControl?` (`String|Pattern`): Sub-control name to append to the control key. Aliases: sc
- `config.depth?` (`Number|Pattern`): Relative modulation depth. Aliases: dep, dr
- `config.depthabs?` (`Number|Pattern`): Absolute modulation depth. Aliases: da
- `config.attack?` (`Number|Pattern`): Time to reach depth. Aliases: att, a
- `config.decay?` (`Number|Pattern`): Time to reach sustain. Aliases: dec, d
- `config.sustain?` (`Number|Pattern`): Sustain depth. Aliases: sus, s
- `config.release?` (`Number|Pattern`): Time to return to nominal value. Aliases: rel, r
- `config.acurve?` (`Number|Pattern`): Snappiness of attack curve (-1 = relaxed, 1 = snappy). Aliases: ac
- `config.dcurve?` (`Number|Pattern`): Snappiness of decay curve (-1 = relaxed, 1 = snappy). Aliases: dc
- `config.rcurve?` (`Number|Pattern`): Snappiness of release curve (-1 = relaxed, 1 = snappy). Aliases: rc
- `config.fxi?` (`Number|Pattern`): FX index to target
- `id` (`String|Pattern`): ID to use for this modulator

**Examples**

```js
s("saw").note("F1").lpf(500).env({ a: 1 })
```

```js
s("saw").env({ d: 1 }).note("F1")
  .lpq(4).lpf(50)
  .env({ a: 0.1, d: 1, ac: 0.8, dc: 0.3, depth: 50 })
```

```js
s("saw").lpf(500).diode(0.3)
  .env({ c: "lpf", a: 0.5, d: 0.5 })
```

```js
s("pulse").lpf(500).env({ a: 1 })
  .env({ c: "s", a: 1 })
  .diode(0.3)
  .sometimes(x => x.env({ a: "0.5" }, 1)) // envelope #1 (0-indexed)
```

```js
s("pulse").lpf(500).env({ a: 1 }, 'lpf_mod')
  .env({ c: "s", a: 1 })
  .diode(0.3)
  .sometimes(x => x.env({ a: "0.5" }, 'lpf_mod'))
```

### euclid

`euclid(pulses: Number, steps: Number) -> Pattern`

Pattern method — call it on a pattern: `pattern.euclid(...)`.

Changes the structure of the pattern to form an Euclidean rhythm.
Euclidean rhythms are rhythms obtained using the greatest common
divisor of two numbers.  They were described in 2004 by Godfried
Toussaint, a Canadian computer scientist.  Euclidean rhythms are
really useful for computer/algorithmic music because they can
describe a large number of rhythms with a couple of numbers.

**Parameters**

- `pulses` (`Number`): the number of onsets/beats
- `steps` (`Number`): the number of steps to fill

**Examples**

```js
// The Cuban tresillo pattern.
note("c3").euclid(3,8)
```

### euclidish

`euclidish(pulses: Number, steps: Number, groove: Number)`

Pattern method — call it on a pattern: `pattern.euclidish(...)`.

Synonyms: eish

A 'euclid' variant with an additional parameter that morphs the resulting
rhythm from 0 (no morphing) to 1 (completely 'even'). For example
`sound("bd").euclidish(3,8,0)` would be the same as
`sound("bd").euclid(3,8)`, and `sound("bd").euclidish(3,8,1)` would be the
same as `sound("bd bd bd")`. `sound("bd").euclidish(3,8,0.5)` would have a
groove somewhere between.
Inspired by the work of Malcom Braff.

**Parameters**

- `pulses` (`Number`): the number of onsets
- `steps` (`Number`): the number of steps to fill
- `groove` (`Number`): exists between the extremes of 0 (straight euclidian) and 1 (straight pulse)

**Examples**

```js
sound("hh").euclidish(7,12,sine.slow(8))
.pan(sine.slow(8))
```

### euclidLegato

`euclidLegato(pulses: Number, steps: Number, rotation, pat)`

Pattern method — call it on a pattern: `pattern.euclidLegato(...)`.

Similar to `euclid`, but each pulse is held until the next pulse,
so there will be no gaps.

**Parameters**

- `pulses` (`Number`): the number of onsets/beats
- `steps` (`Number`): the number of steps to fill
- `rotation`: offset in steps
- `pat`

**Examples**

```js
note("c3").euclidLegato(3,8)
```

### euclidLegatoRot

`euclidLegatoRot(pulses: Number, steps: Number, rotation: Number)`

Pattern method — call it on a pattern: `pattern.euclidLegatoRot(...)`.

Similar to `euclid`, but each pulse is held until the next pulse,
so there will be no gaps, and has an additional parameter for 'rotating'
the resulting sequence

**Parameters**

- `pulses` (`Number`): the number of onsets/beats
- `steps` (`Number`): the number of steps to fill
- `rotation` (`Number`): offset in steps

**Examples**

```js
note("c3").euclidLegatoRot(3,5,2)
```

### euclidRot

`euclidRot(pulses: Number, steps: Number, rotation: Number) -> Pattern`

Pattern method — call it on a pattern: `pattern.euclidRot(...)`.

Like `euclid`, but has an additional parameter for 'rotating' the resulting sequence.

**Parameters**

- `pulses` (`Number`): the number of onsets/beats
- `steps` (`Number`): the number of steps to fill
- `rotation` (`Number`): offset in steps

**Examples**

```js
// A Samba rhythm necklace from Brazil
note("c3").euclidRot(3,16,14)
```

### every

`every(n: Number, func: Function) -> Pattern`

Pattern method — call it on a pattern: `pattern.every(...)`.

An alias for `firstOf`

**Parameters**

- `n` (`Number`): how many cycles
- `func` (`Function`): function to apply

**Examples**

```js
note("c3 d3 e3 g3").every(4, x=>x.rev())
```

### expand

`expand()`

*Experimental*

Expands the step size of the pattern by the given factor.

**Examples**

```js
sound("tha dhi thom nam").bank("mridangam").expand("3 2 1 1 2 3").pace(8)
```

### extend

`extend()`

*Experimental*

`extend` is similar to `fast` in that it increases its density, but it also increases the step count
accordingly. So `stepcat("a b".extend(2), "c d")` would be the same as `"a b a b c d"`, whereas
`stepcat("a b".fast(2), "c d")` would be the same as `"[a b] [a b] c d"`.

**Examples**

```js
stepcat(
  sound("bd bd - cp").extend(2),
  sound("bd - sd -")
).pace(8)
```

### fanchor

`fanchor(center: Number|Pattern)`

controls the center of the filter envelope. 0 is unipolar positive, .5 is bipolar, 1 is unipolar negative

**Parameters**

- `center` (`Number|Pattern`): 0 to 1

**Examples**

```js
note("{f g g c d a a#}%8").s("sawtooth").lpf("{1000}%2")
.lpenv(8).fanchor("<0 .5 1>")
```

### fast

`fast(factor: Number|Pattern) -> Pattern`

Pattern method — call it on a pattern: `pattern.fast(...)`.

Synonyms: density

Speed up a pattern by the given factor. Used by "*" in mini notation.

**Parameters**

- `factor` (`Number|Pattern`): speed up factor

**Examples**

```js
s("bd hh sd hh").fast(2) // s("[bd hh sd hh]*2")
```

### fastChunk

`fastChunk() -> Pattern`

Pattern method — call it on a pattern: `pattern.fastChunk(...)`.

Synonyms: fastchunk

Like `chunk`, but the cycles of the source pattern aren't repeated
for each set of chunks.

**Examples**

```js
"<0 8> 1 2 3 4 5 6 7"
.scale("C2:major").note()
.fastChunk(4, x => x.color('red')).slow(2)
```

### fastGap

`fastGap()`

Synonyms: fastgap

speeds up a pattern like fast, but rather than it playing multiple times as fast would it instead leaves a gap in the remaining space of the cycle. For example, the following will play the sound pattern "bd sn" only once but compressed into the first half of the cycle, i.e. twice as fast.

**Examples**

```js
s("bd sd").fastGap(2)
```

### filter

`filter(test: Function)`

Filters haps using the given function

**Parameters**

- `test` (`Function`): function to test Hap

**Examples**

```js
s("hh!7 oh").filter(hap => hap.value.s === 'hh')
```

### filterHaps

`filterHaps(hap_test: Function) -> Pattern`

Pattern method — call it on a pattern: `pattern.filterHaps(...)`.

Returns a new Pattern, which only returns haps that meet the given test.

**Parameters**

- `hap_test` (`Function`): a function which returns false for haps to be removed from the pattern

**Examples**

```js
s("bd*8").velocity(rand).filterHaps((h) => (h.whole.begin % 1) < h.value.velocity)
```

### filterValues

`filterValues(value_test: Function) -> Pattern`

Pattern method — call it on a pattern: `pattern.filterValues(...)`.

As with `filterHaps`, but the function is applied to values
inside haps.

**Parameters**

- `value_test` (`Function`)

**Examples**

```js
const drums = s("bd sd bd sd")
kick: drums.filterValues((v) => v.s === 'bd').duck(2)
snare: drums.filterValues((v) => v.s === 'sd')
bass: s("saw!4").note("G#1").lpf(80).lpenv(4).orbit(2)
```

### filterWhen

`filterWhen(test: Function)`

Filters haps by their begin time

**Parameters**

- `test` (`Function`): function to test Hap.whole.begin

**Examples**

```js
oneCycle: s("bd*4").filterWhen((t) => t < 1)
```

### firstCycle

`firstCycle(with_context: Boolean)`

Pattern method — call it on a pattern: `pattern.firstCycle(...)`.

Queries the pattern for the first cycle, returning Haps. Mainly of use when
debugging a pattern.

**Parameters**

- `with_context` (`Boolean`): set to true, otherwise the context field will be stripped from the resulting haps.

**Returns** — [Hap]

### firstCycleValues

`firstCycleValues`

Read it from a pattern: `pattern.firstCycleValues` (an accessor, not a method — do not call it).

Accessor for a list of values returned by querying the first cycle.

### firstOf

`firstOf(n: Number, func: Function) -> Pattern`

Pattern method — call it on a pattern: `pattern.firstOf(...)`.

Applies the given function every n cycles, starting from the first cycle.

**Parameters**

- `n` (`Number`): how many cycles
- `func` (`Function`): function to apply

**Examples**

```js
note("c3 d3 e3 g3").firstOf(4, x=>x.rev())
```

### fit

`fit()`

Makes the sample fit its event duration. Good for rhythmical loops like drum breaks.
Similar to `loopAt`.

**Examples**

```js
samples({ rhodes: 'https://cdn.freesound.org/previews/132/132051_316502-lq.mp3' })
s("rhodes/2").fit()
```

### floor

`floor() -> Pattern`

Pattern method — call it on a pattern: `pattern.floor(...)`.

Assumes a numerical pattern. Returns a new pattern with all values set to
their mathematical floor. E.g. `3.7` replaced with to `3`, and `-4.2`
replaced with `-5`.

**Examples**

```js
note("42 42.1 42.5 43".floor())
```

### fmattack

`fmattack(time: Number|Pattern)`

Synonyms: fmatt

Attack time for the FM envelope: time it takes to reach maximum modulation

A number may be added afterwards to control the attack of the envelope of
any of the 8 individual FMs (e.g. `fmatt5`)

**Parameters**

- `time` (`Number|Pattern`): attack time

**Examples**

```js
note("c e g b g e")
.fm(4)
.fmattack("<0 .05 .1 .2>")
._scope()
```

### fmdecay

`fmdecay(time: Number|Pattern)`

Synonyms: fmdec

Decay time for the FM envelope: seconds until the sustain level is reached after the attack phase.

A number may be added afterwards to control the decay of the envelope of
any of the 8 individual FMs (e.g. `fmdec6`)

**Parameters**

- `time` (`Number|Pattern`): decay time

**Examples**

```js
note("c e g b g e")
.fm(4)
.fmdecay("<.01 .05 .1 .2>")
.fmsustain(.4)
._scope()
```

### fmenv

`fmenv(type: Number|Pattern)`

Synonyms: fme

Ramp type of fm envelope. Exp might be a bit broken..

A number may be added afterwards to control the envelope of
any of the 8 individual FMs (e.g. `fmenv4`)

**Parameters**

- `type` (`Number|Pattern`): lin | exp

**Examples**

```js
note("c e g b g e")
.fm(4)
.fmdecay(.2)
.fmsustain(0)
.fmenv("<exp lin>")
._scope()
```

### fmh

`fmh(harmonicity: Number|Pattern)`

Sets the Frequency Modulation Harmonicity Ratio.
Controls the timbre of the sound.
Whole numbers and simple ratios sound more natural,
while decimal numbers and complex ratios sound metallic.

A number may be added afterwards to control the harmonicity of
any of the 8 individual FMs (e.g. `fmh2`)

**Parameters**

- `harmonicity` (`Number|Pattern`)

**Examples**

```js
note("c e g b g e")
.fm(4)
.fmh("<1 2 1.5 1.61>")
._scope()
```

### fmi

`fmi(brightness: Number|Pattern)`

Synonyms: fm

Sets the Frequency Modulation of the synth.
Controls the modulation index, which defines the brightness of the sound.

A number may be added afterwards to control the modulation index of
any of the 8 individual FMs (e.g. `fm3`). Also, FMs may be routed into
each other with matrix commands like `fm13`, which would send `fm1` back into
`fm3`

**Parameters**

- `brightness` (`Number|Pattern`): modulation index

**Examples**

```js
note("c e g b g e")
.fm("<0 1 2 8 32>")
._scope()
```

```js
s("sine").note("F1").seg(8)
 .fm(4).fm2(rand.mul(4)).fm3(saw.mul(8).slow(8))
 .fmh(1.06).fmh2(10).fmh3(0.1)
```

### fmrelease

`fmrelease(time: Number|Pattern)`

Synonyms: fmrel

Release time for the FM envelope: how much modulation is applied after the note is released

A number may be added afterwards to control the release of the envelope of
any of the 8 individual FMs (e.g. `fmrel8`)

**Parameters**

- `time` (`Number|Pattern`): release time

### fmsustain

`fmsustain(level: Number|Pattern)`

Synonyms: fmsus

Sustain level for the FM envelope: how much modulation is applied after the decay phase

A number may be added afterwards to control the sustain of the envelope of
any of the 8 individual FMs (e.g. `fmsus7`)

**Parameters**

- `level` (`Number|Pattern`): sustain level

**Examples**

```js
note("c e g b g e")
.fm(4)
.fmdecay(.1)
.fmsustain("<1 .75 .5 0>")
._scope()
```

### fmwave

`fmwave(wave: Number|Pattern)`

Waveform of the fm modulator

A number may be added afterwards to control the waveform
any of the 8 individual FMs (e.g. `fmwave6`)

**Parameters**

- `wave` (`Number|Pattern`): waveform

**Examples**

```js
n("0 1 2 3".fast(4)).scale("d:minor").s("sine").fmwave("<sine square sawtooth crackle>").fm(4).fmh(2.01)
```

```js
n("0 1 2 3".fast(4)).chord("<Dm Am F G>").voicing().s("sawtooth").fmwave("brown").fm(.6)
```

### focus

`focus()`

Similar to `compress`, but doesn't leave gaps, and the 'focus' can be bigger than a cycle

**Examples**

```js
s("bd hh sd hh").focus(1/4, 3/4)
```

### fold

`fold(distortion: Number|Pattern, volume: Number|Pattern)`

Wavefolding distortion

**Parameters**

- `distortion` (`Number|Pattern`): amount of distortion to apply
- `volume` (`Number|Pattern`): linear postgain of the distortion

### freq

`freq(frequency: Number|Pattern)`

Set frequency of sound.

**Parameters**

- `frequency` (`Number|Pattern`): in Hz. the audible range is between 20 and 20000 Hz

**Examples**

```js
freq("220 110 440 110").s("superzow").osc()
```

```js
freq("110".mul.out(".5 1.5 .6 [2 3]")).s("superzow").osc()
```

### fromBipolar

`fromBipolar() -> Pattern`

Assumes a numerical pattern, containing bipolar values in the range -1 .. 1
Returns a new pattern with values scaled to the unipolar range 0 .. 1

### ftype

`ftype(type: Number|Pattern)`

Sets the filter type. The ladder filter is more aggressive. More types might be added in the future.

**Parameters**

- `type` (`Number|Pattern`): 12db (0), ladder (1), or 24db (2)

**Examples**

```js
note("{f g g c d a a#}%8").s("sawtooth").lpenv(4).lpf(500).ftype("<0 1 2>").lpq(1)
```

```js
note("c f g g a c d4").fast(2)
.sound('sawtooth')
.lpf(200).fanchor(0)
.lpenv(3).lpq(1)
.ftype("<ladder 12db 24db>")
```

### FX

`FX() -> Pattern`

Pattern method — call it on a pattern: `pattern.FX(...)`.

Establishes an FX chain. Can be called by chaining .FX(fx1).FX(fx2)..
calls and/or in a single .FX(fx1, fx2, ..) call. The fx1, .. are *patterns* which
establish the controls of the given effect. See examples.

**Examples**

```js
$: s("[sbd <hh [bd | lt | oh]>]*4").dec(.4)
  .FX(
    phaser(0.5).gain(2),
    bpf(800),
    distort(1.3),
    room(0.2),
    delay(0.5).gain(1.25),
    distort(0.3),
  ).fxr(1.7) // sets release time of effects (like delay)
```

```js
$: s("saw").fm(0.5)
  .delay(0.3) // outer effects are applied *last*
  .FX(coarse(4)) // first coarse
  .FX(lpf(500).lpe(4).lpa(1).lpd(2)) // then lpf
  .FX(distort(1)) // then distort
```

### gain

`gain(amount: Number|Pattern)`

Controls the gain by an exponential amount.

**Parameters**

- `amount` (`Number|Pattern`): gain.

**Examples**

```js
s("hh*8").gain(".4!2 1 .4!2 1 .4 1").fast(2)
```

### gap

`gap(steps: Number)`

Does absolutely nothing, but with a given metrical 'steps'

**Parameters**

- `steps` (`Number`)

**Examples**

```js
gap(3) // "~@3"
```

### grow

`grow() -> Pattern`

*Experimental*

Progressively grows the pattern by 'n' steps until the full pattern is played, or if a second value is given (using mininotation list syntax with `:`),
that number of times.
A positive number will progressively grow steps from the start of a pattern, and a negative number from the end.

**Examples**

```js
"tha dhi thom nam".grow("1").sound()
.bank("mridangam")
```

```js
"tha dhi thom nam".grow("-1").sound()
.bank("mridangam")
```

```js
"tha dhi thom nam".grow("1 -1").sound().bank("mridangam").pace(4)
```

```js
note("0 1 2 3 4 5 6 7".scale("C:ritusen")).sound("folkharp")
   .grow("1 -1").pace(8)
```

### hard

`hard(distortion: Number|Pattern, volume: Number|Pattern)`

Hard-clipping distortion

**Parameters**

- `distortion` (`Number|Pattern`): amount of distortion to apply
- `volume` (`Number|Pattern`): linear postgain of the distortion

### hpattack

`hpattack(attack: Number|Pattern)`

Synonyms: hpa

Sets the attack duration for the highpass filter envelope.

**Parameters**

- `attack` (`Number|Pattern`): time of the highpass filter envelope

**Examples**

```js
note("c2 e2 f2 g2")
.sound('sawtooth')
.hpf(500)
.hpa("<.5 .25 .1 .01>/4")
.hpenv(4)
```

### hpdc

`hpdc(dcoffset: Number|Pattern)`

DC offset of the LFO for the highpass filter

**Parameters**

- `dcoffset` (`Number|Pattern`): dc offset. set to 0 for unipolar

### hpdecay

`hpdecay(decay: Number|Pattern)`

Synonyms: hpd

Sets the decay duration for the highpass filter envelope.

**Parameters**

- `decay` (`Number|Pattern`): time of the highpass filter envelope

**Examples**

```js
note("c2 e2 f2 g2")
.sound('sawtooth')
.hpf(500)
.hpd("<.5 .25 .1 0>/4")
.hps(0.2)
.hpenv(4)
```

### hpdepth

`hpdepth(depth: Number|Pattern)`

Depth of the LFO for the highpass filter

**Parameters**

- `depth` (`Number|Pattern`): depth of modulation

### hpdepthfrequency

`hpdepthfrequency(depth: Number|Pattern)`

Synonyms: hpdepthfreq

Depth of the LFO for the hipass filter, in hz

**Parameters**

- `depth` (`Number|Pattern`): depth of modulation

**Examples**

```js
note("<c c c# c c c4>*16").s("sawtooth").lpf(600).hpdepthfrequency("<200 500 100 0>")
```

### hpenv

`hpenv(modulation: Number|Pattern)`

Synonyms: hpe

Sets the highpass filter envelope modulation depth.

**Parameters**

- `modulation` (`Number|Pattern`): depth of the highpass filter envelope between 0 and *n*

**Examples**

```js
note("c2 e2 f2 g2")
.sound('sawtooth')
.hpf(500)
.hpa(.5)
.hpenv("<4 2 1 0 -1 -2 -4>/4")
```

### hpf

`hpf(frequency: Number|Pattern)`

Synonyms: hp, hcutoff

Applies the cutoff frequency of the **h**igh-**p**ass **f**ilter.

When using mininotation, you can also optionally add the 'hpq' parameter, separated by ':'.

**Parameters**

- `frequency` (`Number|Pattern`): audible between 0 and 20000

**Examples**

```js
s("bd sd [~ bd] sd,hh*8").hpf("<4000 2000 1000 500 200 100>")
```

```js
s("bd sd [~ bd] sd,hh*8").hpf("<2000 2000:25>")
```

### hpq

`hpq(q: Number|Pattern)`

Synonyms: hresonance

Controls the **h**igh-**p**ass **q**-value.

**Parameters**

- `q` (`Number|Pattern`): resonance factor between 0 and 50

**Examples**

```js
s("bd sd [~ bd] sd,hh*8").hpf(2000).hpq("<0 10 20 30>")
```

### hprate

`hprate(rate: Number|Pattern)`

Rate of the LFO for the highpass filter

**Parameters**

- `rate` (`Number|Pattern`): rate in hertz

### hprelease

`hprelease(release: Number|Pattern)`

Synonyms: hpr

Sets the release time for the highpass filter envelope.

**Parameters**

- `release` (`Number|Pattern`): time of the highpass filter envelope

**Examples**

```js
note("c2 e2 f2 g2")
.sound('sawtooth')
.clip(.5)
.hpf(500)
.hpenv(4)
.hpr("<.5 .25 .1 0>/4")
.release(.5)
```

### hpshape

`hpshape(shape: Number|Pattern)`

Shape of the LFO for the highpass filter

**Parameters**

- `shape` (`Number|Pattern`): Shape of the lfo (0, 1, 2,..)

### hpskew

`hpskew(skew: Number|Pattern)`

Skew of the LFO for the highpass filter

**Parameters**

- `skew` (`Number|Pattern`): How much to bend the LFO shape

### hpsustain

`hpsustain(sustain: Number|Pattern)`

Synonyms: hps

Sets the sustain amplitude for the highpass filter envelope.

**Parameters**

- `sustain` (`Number|Pattern`): amplitude of the highpass filter envelope

**Examples**

```js
note("c2 e2 f2 g2")
.sound('sawtooth')
.hpf(500)
.hpd(.5)
.hps("<0 .25 .5 1>/4")
.hpenv(4)
```

### hpsync

`hpsync(rate: Number|Pattern)`

Cycle-synced rate of the LFO for the highpass filter

**Parameters**

- `rate` (`Number|Pattern`): rate in cycles

### hurry

`hurry()`

Both speeds up the pattern (like 'fast') and the sample playback (like 'speed').

**Examples**

```js
s("bd sd:2").hurry("<1 2 4 3>").slow(1.5)
```

### hush

`hush()`

Pattern method — call it on a pattern: `pattern.hush(...)`.

Silences a pattern.

**Examples**

```js
stack(
  s("bd").hush(),
  s("hh*3")
)
```

### i

`i(value: Number|Pattern)`

Selects the given degree. Currently used in `xen` and `tune`:

**Parameters**

- `value` (`Number|Pattern`)

**Examples**

```js
i("0 1 2 3 4 5 6 7").xen("<5edo 10edo 15edo hexany15>")
```

### inhabit

`inhabit(pat: Pattern, xs: *) -> Pattern`

Synonyms: pickSqueeze

Picks patterns (or plain values) either from a list (by index) or a lookup table (by name).
Similar to `pick`, but cycles are squeezed into the target ('inhabited') pattern.

**Parameters**

- `pat` (`Pattern`)
- `xs` (`*`)

**Examples**

```js
let a = s("bd(3,8)")
let b = s("cp sd")
"<a b [a,b]>".inhabit({ a, b })
```

```js
s("a@2 [a b] a"
.inhabit({a: "bd(3,8)", b: "sd sd"}))
.slow(4)
```

### inhabitmod

`inhabitmod(pat: Pattern, xs: *) -> Pattern`

Synonyms: pickmodSqueeze

The same as `inhabit`, but if you pick a number greater than the size of the list,
it wraps around, rather than sticking at the maximum value.
For example, if you pick the fifth pattern of a list of three, you'll get the
second one.

**Parameters**

- `pat` (`Pattern`)
- `xs` (`*`)

### inside

`inside()`

Carries out an operation 'inside' a cycle.

**Examples**

```js
"0 1 2 3 4 3 2 1".inside(4, rev).scale('C major').note()
// "0 1 2 3 4 3 2 1".slow(4).rev().fast(4).scale('C major').note()
```

### into

`into()`

Pattern method — call it on a pattern: `pattern.into(...)`.

Breaks a pattern into pieces according to the structure of a given pattern.
True values in the given pattern cause the corresponding subcycle of the
source pattern to be looped, and for an (optional) given function to be
applied. False values result in the corresponding part of the source pattern
to be played unchanged.

**Examples**

```js
sound("bd sd ht lt").into("1 0", hurry(2))
```

### invert

`invert()`

Synonyms: inv

Swaps 1s and 0s in a binary pattern.

**Examples**

```js
s("bd").struct("1 0 0 1 0 0 1 0".lastOf(4, invert))
```

### irand

`irand(n: Number)`

A continuous pattern of random integers, between 0 and n-1.

**Parameters**

- `n` (`Number`): max value (exclusive)

**Examples**

```js
// randomly select scale notes from 0 - 7 (= C to C)
n(irand(8)).struct("x x*2 x x*3").scale("C:minor")
```

### irbegin

`irbegin(begin: String|Pattern)`

Synonyms: ir

Sets the beginning of the IR response sample

**Parameters**

- `begin` (`String|Pattern`): between 0 and 1

**Examples**

```js
samples('github:switchangel/pad')
$: s("brk/2").fit().scrub(irand(16).div(16).seg(8)).ir("swpad:4").room(.65).irspeed("-2").irbegin("<0 .5 .75>/2").roomsize(.6)
```

### iresponse

`iresponse(sample: String|Pattern)`

Synonyms: ir

Sets the sample to use as an impulse response for the reverb.

**Parameters**

- `sample` (`String|Pattern`): to use as an impulse response

**Examples**

```js
s("bd sd [~ bd] sd").room(.8).ir("<shaker_large:0 shaker_large:2>")
```

### irspeed

`irspeed(speed: String|Pattern)`

Sets speed of the sample for the impulse response.

**Parameters**

- `speed` (`String|Pattern`)

**Examples**

```js
samples('github:switchangel/pad')
$: s("brk/2").fit().scrub(irand(16).div(16).seg(8)).ir("swpad:4").room(.2).irspeed("<2 1 .5>/2").irbegin(.5).roomsize(.5)
```

### isaw

`isaw` — a Pattern value, not a function. Use it directly, e.g. `isaw.range(...)`.

A sawtooth signal between 1 and 0 (like `saw`, but flipped).

**Examples**

```js
note("<c3 [eb3,g3] g2 [g3,bb3]>*8")
.clip(isaw.slow(2))
```

```js
n(isaw.range(0,8).segment(8))
.scale('C major')
```

### isaw2

`isaw2` — a Pattern value, not a function. Use it directly, e.g. `isaw2.range(...)`.

A sawtooth signal between 1 and -1 (like `saw2`, but flipped).

### isquare

`isquare` — a Pattern value, not a function. Use it directly, e.g. `isquare.range(...)`.

A square signal between 1 and 0 (like `square` but flipped).

### isquare2

`isquare2` — a Pattern value, not a function. Use it directly, e.g. `isquare2.range(...)`.

A square signal between 1 and -1 (like `isquare`, but bipolar).

### iter

`iter() -> Pattern`

Pattern method — call it on a pattern: `pattern.iter(...)`.

Divides a pattern into a given number of subdivisions, plays the subdivisions in order, but increments the starting subdivision each cycle. The pattern wraps to the first subdivision after the last subdivision is played.

**Examples**

```js
note("0 1 2 3".scale('A minor')).iter(4)
```

### iterBack

`iterBack() -> Pattern`

Pattern method — call it on a pattern: `pattern.iterBack(...)`.

Synonyms: iterback

Like `iter`, but plays the subdivisions in reverse order. Known as iter' in tidalcycles

**Examples**

```js
note("0 1 2 3".scale('A minor')).iterBack(4)
```

### itri

`itri` — a Pattern value, not a function. Use it directly, e.g. `itri.range(...)`.

An inverted triangle signal between 1 and 0 (like `tri`, but flipped).

**Examples**

```js
n(itri.segment(8).range(0,7)).scale("C:minor")
```

### itri2

`itri2` — a Pattern value, not a function. Use it directly, e.g. `itri2.range(...)`.

An inverted triangle signal between -1 and 1 (like `itri`, but bipolar).

### jux

`jux()`

The jux function creates strange stereo effects, by applying a function to a pattern, but only in the right-hand channel.

**Examples**

```js
s("bd lt [~ ht] mt cp ~ bd hh").jux(rev)
```

```js
s("bd lt [~ ht] mt cp ~ bd hh").jux(press)
```

```js
s("bd lt [~ ht] mt cp ~ bd hh").jux(iter(4))
```

### juxBy

`juxBy()`

Synonyms: juxby

Jux with adjustable stereo width. 0 = mono, 1 = full stereo.

**Examples**

```js
s("bd lt [~ ht] mt cp ~ bd hh").juxBy("<0 .5 1>/2", rev)
```

### juxFlip

`juxFlip()`

Synonyms: juxflip, flux

Like jux, but flips the ears each cycle.

**Examples**

```js
s("bd lt [~ ht] mt cp ~ bd hh").juxFlip(rev)
```

```js
s("bd lt [~ ht] mt cp ~ bd hh").juxFlip(press)
```

```js
s("bd lt [~ ht] mt cp ~ bd hh").juxFlip(iter(4))
```

### juxFlipBy

`juxFlipBy()`

Synonyms: juxflipby, fluxBy, fluxby

Like juxBy, except it flips the ears each cycle.

**Examples**

```js
s("bd lt [~ ht] mt cp ~ bd hh").juxFlipBy(".8", rev)
```

### K

`K(expr: KabelsalatExpression|Function) -> Pattern`

Pattern method — call it on a pattern: `pattern.K(...)`.

Produces a Kabelsalat (https://kabel.salat.dev/) modular sound engine.
This can be used as either an effect (by including `audioin()` at the beginning
of your kabel expression) or as a sound source (via any expression which doesn't
start with `audioin()`).

Some helpers you have available to you:

- Strudel mini notation works fine in K(..) via "" or ``
- More complex Strudel expressions (like "0 1 2".fast(4) or irand(24)) can be
written by wrapping them in `S(..)` inside your Kabel code
- We expose Strudel's note frequency under `sFreq` and Strudel's gate
information under `sGate`
- You can use more complex multi-line expressions (like `let x = a; let y = b; x.lpf(y);`)
by wrapping them inside a function in K (see example).

**Parameters**

- `expr` (`KabelsalatExpression|Function`): Kabelsalat graph definition

**Examples**

```js
note("A c e".fast(4)).transpose("<0 2 4 6 8>")
  .scale("F:minor").transpose("12")
  .s("saw")
  .K(
    // audioin().mul(sGate.adsr(0.001, 0.3, 0, 0.2)) // as effect
    saw(saw(sFreq / "2!3 16").mul(8).add(sFreq).lag("0!3 0.1")).mul(0.3) // as source
    .mul(sGate.adsr(0, 0.15, 0.5, "0.1!3 1"))
    .lpf(sGate.adsr(0, 0.2, 0.3, 0.2).mul(1).add(0))
    .add(x => x.delay(S("0.3 0.2".fast(2))).mul(0.7))
    .add(x => x.delay("0.03 [0.08 0.01] 0.01 0.013").mul(0.77)).mul(0.7)
    .add(x => x.delay(.13).mul(0.7))
    .out()
  )
```

```js
n("<0 1 <2 3 2 4>>*16")
  .scale("G#2:minor").sometimes(x => x.transpose("12 | 24"))
  .K(() => {
    const att = S(rand.range(0, 0.05))
    const dec = S(rand.range(0.05, 0.2))
    let f = n(sFreq);
    const mod = sine(f).mul("0.1 | 0.2 | 0.3")
      .add("[[1.5 1] | 1 | 2 | 4 | [6 4@3]]*2")
    saw(f.mul(mod))
    .mul(sGate.ad(att, dec))
    .add(x => x.delay(0.4).mul(0.3))
    .out()
  }).fxr(1).room(0.3)
```

### keep

`keep(pat: Pattern) -> Pattern`

Pattern method — call it on a pattern: `pattern.keep(...)`.

When called on a pattern `a`, with a input pattern `b` (`a.keep(b)`),
combines `a` and `b` such that anything defined in `a`,
and anything defined in `b` that is *not* defined in `a`
will be in the resulting pattern

The structure is maintained from `a`,
because the default pattern alignment is `in`,
see the section on `Pattern Alignment`
in the technical manual in the docs

This is the inverse of `set`

See examples below

**Parameters**

- `pat` (`Pattern`)

**Examples**

```js
// notes, already defined, will stay "c a f e",
// while "s", not defined, will be set to "piano"
note("c a f e").keep(note("e f a c").s("piano"))
```

### keyDown

`keyDown() -> Pattern`

Pattern method — call it on a pattern: `pattern.keyDown(...)`.

returns true when a key or array of keys is held
Key name reference (https://developer.mozilla.org/en-US/docs/Web/API/UI_Events/Keyboard_event_key_values)

**Examples**

```js
keyDown("Control:j").pick([s("bd(5,8)"), s("cp(3,8)")])
```

### label

`label(label: String)`

Sets the displayed text for an event on the pianoroll

**Parameters**

- `label` (`String`): text to display

### lastOf

`lastOf(n: Number, func: Function) -> Pattern`

Pattern method — call it on a pattern: `pattern.lastOf(...)`.

Applies the given function every n cycles, starting from the last cycle.

**Parameters**

- `n` (`Number`): how many cycles
- `func` (`Function`): function to apply

**Examples**

```js
note("c3 d3 e3 g3").lastOf(4, x=>x.rev())
```

### late

`late(cycles: Number|Pattern) -> Pattern`

Pattern method — call it on a pattern: `pattern.late(...)`.

Nudge a pattern to start later in time. Equivalent of Tidal's ~> operator

**Parameters**

- `cycles` (`Number|Pattern`): number of cycles to nudge right

**Examples**

```js
"bd ~".stack("hh ~".late(.1)).s()
```

### layer

`layer() -> Pattern`

Pattern method — call it on a pattern: `pattern.layer(...)`.

Layers the result of the given function(s). Like `superimpose`, but without the original pattern:

**Examples**

```js
"<0 2 4 6 ~ 4 ~ 2 0!3 ~!5>*8"
  .layer(x=>x.add("0,2"))
  .scale('C minor').note()
```

### lfo

`lfo(config: Object, config.control?: String|Pattern, config.subControl?: String|Pattern, config.rate?: Number|Pattern, config.sync?: Number|Pattern, config.depth?: Number|Pattern, config.depthabs?: Number|Pattern, config.dcoffset?: Number|Pattern, config.shape?: Number|Pattern, config.skew?: Number|Pattern, config.curve?: Number|Pattern, config.retrig?: Number|Pattern, config.fxi?: Number|Pattern, id: String|Pattern) -> Pattern`

Configures an LFO. Can be called in sequence like pat.lfo(...).lfo(...) to set up multiple LFOs.
There are two ways to declare which control will be modulated:

1. Explicitly put `control` in the config (e.g. `lfo({ c: "lpf" })`)
2. If the control parameter is absent, the control *immediately before* the `lfo` call will be used
(e.g. `s("saw").lpf(500).lfo()` to modulate `lpf`)

Modulators can be referred to by `id` so that they can be updated later e.g. inside
a `sometimes`. See example below.

**Parameters**

- `config` (`Object`): LFO configuration.
- `config.control?` (`String|Pattern`): Node to modulate. Aliases: c
- `config.subControl?` (`String|Pattern`): Sub-control name to append to the control key. Aliases: sc
- `config.rate?` (`Number|Pattern`): Modulation rate. Aliases: r
- `config.sync?` (`Number|Pattern`): Tempo-synced modulation rate. Aliases: s
- `config.depth?` (`Number|Pattern`): Relative modulation depth. Aliases: dep, dr
- `config.depthabs?` (`Number|Pattern`): Absolute modulation depth. Aliases: da
- `config.dcoffset?` (`Number|Pattern`): DC offset / bias for the waveform. Aliases: dc
- `config.shape?` (`Number|Pattern`): Shape index. Aliases: sh
- `config.skew?` (`Number|Pattern`): Skew amount. Aliases: sk
- `config.curve?` (`Number|Pattern`): Exponential curve amount. Aliases: cu
- `config.retrig?` (`Number|Pattern`): If > 0.5, the LFO will retrigger on each event. Aliases: rt
- `config.fxi?` (`Number|Pattern`): FX index to target
- `id` (`String|Pattern`): ID to use for this modulator

**Examples**

```js
s("saw").note("F1").lpf(500).lfo()
```

```js
s("saw").lfo().lpf(500).lfo({ s: 0.3 })
```

```js
s("saw").lpf(500).diode(0.3)
  .lfo({ c: "lpf" })
```

```js
s("pulse").lpf(500).lfo()
  .lfo({ c: "s" })
  .diode(0.3)
  .sometimes(x => x.lfo({ s: "8" }, 1)) // lfo #1 (0-indexed)
```

```js
s("pulse").lpf(500).lfo({ depth: 4 }, 'lpf_mod')
  .lfo({ c: "s" })
  .diode(0.3)
  .sometimes(x => x.lfo({ s: "8" }, 'lpf_mod'))
```

### linger

`linger(fraction: Number)`

Selects the given fraction of the pattern and repeats that part to fill the remainder of the cycle.

**Parameters**

- `fraction` (`Number`): fraction to select

**Examples**

```js
s("lt ht mt cp, [hh oh]*2").linger("<1 .5 .25 .125>")
```

### log

`log()`

Pattern method — call it on a pattern: `pattern.log(...)`.

Writes the content of the current event to the console (visible in the side menu).

**Examples**

```js
s("bd sd").log()
```

### logValues

`logValues()`

Pattern method — call it on a pattern: `pattern.logValues(...)`.

A simplified version of `log` which writes all "values" (various configurable parameters)
within the event to the console (visible in the side menu).

**Examples**

```js
s("bd sd").gain("0.25 0.5 1").n("2 1 0").logValues()
```

### loop

`loop(on: Number|Pattern)`

Loops the sample.
Note that the tempo of the loop is not synced with the cycle tempo.
To change the loop region, use loopBegin / loopEnd.

**Parameters**

- `on` (`Number|Pattern`): If 1, the sample is looped

**Examples**

```js
s("casio").loop(1)
```

### loopAt

`loopAt() -> Pattern`

Pattern method — call it on a pattern: `pattern.loopAt(...)`.

Makes the sample fit the given number of cycles by changing the speed.

**Examples**

```js
samples({ rhodes: 'https://cdn.freesound.org/previews/132/132051_316502-lq.mp3' })
s("rhodes").loopAt(2)
```

### loopAtCps

`loopAtCps() -> Pattern`

Pattern method — call it on a pattern: `pattern.loopAtCps(...)`.

**Deprecated**: still available in the REPL, but prefer the current alternative.

Makes the sample fit the given number of cycles and cps value, by
changing the speed. deprecated: use loopAt or fit instead, together with setCps / setCpm.

**Examples**

```js
samples({ rhodes: 'https://cdn.freesound.org/previews/132/132051_316502-lq.mp3' })
s("rhodes").loopAtCps(4,1.5).cps(1.5)
```

### loopBegin

`loopBegin(time: Number|Pattern)`

Synonyms: loopb

Begin to loop at a specific point in the sample (inbetween `begin` and `end`).
Note that the loop point must be inbetween `begin` and `end`, and before `loopEnd`!
Note: Samples starting with wt_ will automatically loop! (wt = wavetable)

**Parameters**

- `time` (`Number|Pattern`): between 0 and 1, where 1 is the length of the sample

**Examples**

```js
s("space").loop(1)
.loopBegin("<0 .125 .25>")._scope()
```

### loopEnd

`loopEnd(time: Number|Pattern)`

Synonyms: loope

End the looping section at a specific point in the sample (inbetween `begin` and `end`).
Note that the loop point must be inbetween `begin` and `end`, and after `loopBegin`!

**Parameters**

- `time` (`Number|Pattern`): between 0 and 1, where 1 is the length of the sample

**Examples**

```js
s("space").loop(1)
.loopEnd("<1 .75 .5 .25>")._scope()
```

### lpattack

`lpattack(attack: Number|Pattern)`

Synonyms: lpa

Sets the attack duration for the lowpass filter envelope.

**Parameters**

- `attack` (`Number|Pattern`): time of the filter envelope

**Examples**

```js
note("c2 e2 f2 g2")
.sound('sawtooth')
.lpf(300)
.lpa("<.5 .25 .1 .01>/4")
.lpenv(4)
```

### lpdc

`lpdc(dcoffset: Number|Pattern)`

DC offset of the LFO for the lowpass filter

**Parameters**

- `dcoffset` (`Number|Pattern`): dc offset. set to 0 for unipolar

### lpdecay

`lpdecay(decay: Number|Pattern)`

Synonyms: lpd

Sets the decay duration for the lowpass filter envelope.

**Parameters**

- `decay` (`Number|Pattern`): time of the filter envelope

**Examples**

```js
note("c2 e2 f2 g2")
.sound('sawtooth')
.lpf(300)
.lpd("<.5 .25 .1 0>/4")
.lpenv(4)
```

### lpdepth

`lpdepth(depth: Number|Pattern)`

Depth of the LFO for the lowpass filter

**Parameters**

- `depth` (`Number|Pattern`): depth of modulation

**Examples**

```js
note("<c c c# c c c4>*16").s("sawtooth").lpf(600).lpdepth("<1 .5 1.8 0>")
```

### lpdepthfrequency

`lpdepthfrequency(depth: Number|Pattern)`

Synonyms: lpdepthfreq

Depth of the LFO for the lowpass filter, in HZ

**Parameters**

- `depth` (`Number|Pattern`): depth of modulation

**Examples**

```js
note("<c c c# c c c4>*16").s("sawtooth").lpf(600).lpdepthfrequency("<200 500 100 0>")
```

### lpenv

`lpenv(modulation: Number|Pattern)`

Synonyms: lpe

Sets the lowpass filter envelope modulation depth.

**Parameters**

- `modulation` (`Number|Pattern`): depth of the lowpass filter envelope between 0 and *n*

**Examples**

```js
note("c2 e2 f2 g2")
.sound('sawtooth')
.lpf(300)
.lpa(.5)
.lpenv("<4 2 1 0 -1 -2 -4>/4")
```

### lpf

`lpf(frequency: Number|Pattern)`

Synonyms: cutoff, ctf, lp

Applies the cutoff frequency of the **l**ow-**p**ass **f**ilter.

When using mininotation, you can also optionally add the 'lpq' parameter, separated by ':'.

**Parameters**

- `frequency` (`Number|Pattern`): audible between 0 and 20000

**Examples**

```js
s("bd sd [~ bd] sd,hh*6").lpf("<4000 2000 1000 500 200 100>")
```

```js
s("bd*16").lpf("1000:0 1000:10 1000:20 1000:30")
```

### lpq

`lpq(q: Number|Pattern)`

Synonyms: resonance

Controls the **l**ow-**p**ass **q**-value.

**Parameters**

- `q` (`Number|Pattern`): resonance factor between 0 and 50

**Examples**

```js
s("bd sd [~ bd] sd,hh*8").lpf(2000).lpq("<0 10 20 30>")
```

### lprate

`lprate(rate: Number|Pattern)`

Rate of the LFO for the lowpass filter

**Parameters**

- `rate` (`Number|Pattern`): rate in hertz

**Examples**

```js
note("<c c c# c c c4>*16").s("sawtooth").lpf(600).lprate("<4 8 2 1>")
```

### lprelease

`lprelease(release: Number|Pattern)`

Synonyms: lpr

Sets the release time for the lowpass filter envelope.

**Parameters**

- `release` (`Number|Pattern`): time of the filter envelope

**Examples**

```js
note("c2 e2 f2 g2")
.sound('sawtooth')
.clip(.5)
.lpf(300)
.lpenv(4)
.lpr("<.5 .25 .1 0>/4")
.release(.5)
```

### lpshape

`lpshape(shape: Number|Pattern)`

Shape of the LFO for the lowpass filter

**Parameters**

- `shape` (`Number|Pattern`): Shape of the lfo (0, 1, 2,..)

### lpskew

`lpskew(skew: Number|Pattern)`

Skew of the LFO for the lowpass filter

**Parameters**

- `skew` (`Number|Pattern`): How much to bend the LFO shape

### lpsustain

`lpsustain(sustain: Number|Pattern)`

Synonyms: lps

Sets the sustain amplitude for the lowpass filter envelope.

**Parameters**

- `sustain` (`Number|Pattern`): amplitude of the lowpass filter envelope

**Examples**

```js
note("c2 e2 f2 g2")
.sound('sawtooth')
.lpf(300)
.lpd(.5)
.lps("<0 .25 .5 1>/4")
.lpenv(4)
```

### lpsync

`lpsync(rate: Number|Pattern)`

Cycle-synced rate of the LFO for the lowpass filter

**Parameters**

- `rate` (`Number|Pattern`): rate in cycles

**Examples**

```js
note("<c c c# c c c4>*16").s("sawtooth").lpf(600).lpsync("<4 8 2 1>")
```

### mask

`mask()`

Pattern method — call it on a pattern: `pattern.mask(...)`.

Returns silence when mask is 0 or "~"

**Examples**

```js
note("c [eb,g] d [eb,g]").mask("<1 [0 1]>")
```

### midibend

`midibend(midibend: Number|Pattern)`

MIDI pitch bend: Sends a MIDI pitch bend message.

**Parameters**

- `midibend` (`Number|Pattern`): MIDI pitch bend (-1 - 1)

**Examples**

```js
note("c4").midibend(sine.slow(4).range(-0.4,0.4)).midi()
```

### midichan

`midichan(channel: Number|Pattern)`

MIDI channel: Sets the MIDI channel for the event.

**Parameters**

- `channel` (`Number|Pattern`): MIDI channel number (0-15)

**Examples**

```js
note("c4").midichan(1).midi()
```

### midicmd

`midicmd(command: Number|Pattern)`

MIDI command: Sends a MIDI command message.

**Parameters**

- `command` (`Number|Pattern`): MIDI command

**Examples**

```js
midicmd("clock*48,<start stop>/2").midi()
```

### midiport

`midiport(port: Number|Pattern)`

MIDI port: Sets the MIDI port for the event.

**Parameters**

- `port` (`Number|Pattern`): MIDI port

**Examples**

```js
note("c a f e").midiport("<0 1 2 3>").midi()
```

### miditouch

`miditouch(miditouch: Number|Pattern)`

MIDI key after touch: Sends a MIDI key after touch message.

**Parameters**

- `miditouch` (`Number|Pattern`): MIDI key after touch (0-1)

**Examples**

```js
note("c4").miditouch(sine.slow(4).range(0,1)).midi()
```

### mode

`mode(modeName: String|Pattern)`

How the voicing is aligned to the anchor

- `below`: top note <= anchor
- `duck`: top note <= anchor, anchor excluded
- `above`: bottom note >= anchor
- `root`: bottom note is the lowest root of the chord >= anchor
- `oldabove` : old (buggy) behavior of above, kept for legacy reason
- `oldroot` : old (buggy) behavior of root, kept for legacy reason

**Parameters**

- `modeName` (`String|Pattern`): one of {below | above | duck | root | oldabove | oldroot}

**Examples**

```js
mode("<below above duck root>").chord("C").voicing()
```

### morph

`morph(...)`

Requires arguments: at least 3.

Takes two binary rhythms represented as lists of 1s and 0s, and a number
between 0 and 1 that morphs between them. The two lists should contain the same
number of true values.

**Examples**

```js
sound("hh").struct(morph([1,0,1,0,1,0,1,0], // straight rhythm
                         [1,1,0,1,0,1,0], // wonky rhythm
                         0.25 // creates a slightly wonky rhythm
                        )
                  )
```

```js
sound("hh").struct(morph("1:0:1:0:1:0:1:0", // straight rhythm
                         "1:1:0:1:0:1:0", // wonky rhythm
                         sine.slow(8) // slowly morph between the rhythms
                        )
                  )
```

### mousex

`mousex` — a Pattern value, not a function. Use it directly, e.g. `mousex.range(...)`.

The mouse's x position value ranges from 0 to 1.

**Examples**

```js
n(mousex.segment(4).range(0,7)).scale("C:minor")
```

### mousey

`mousey` — a Pattern value, not a function. Use it directly, e.g. `mousey.range(...)`.

The mouse's y position value ranges from 0 to 1.

**Examples**

```js
n(mousey.segment(4).range(0,7)).scale("C:minor")
```

### mul

`mul()`

Pattern method — call it on a pattern: `pattern.mul(...)`.

Multiplies each number by the given factor.

**Examples**

```js
"<1 1.5 [1.66, <2 2.33>]>*4".mul(150).freq()
```

### n

`n(value: Number|Pattern)`

Selects the given index:

- for samples, it picks the sample by index, with wrap around
- for scales, it picks the scale degree
- for voicings, it picks the voice index

**Parameters**

- `value` (`Number|Pattern`): sample index starting from 0

**Examples**

```js
s("bd sd [~ bd] sd,hh*6").n("<0 1>")
```

### never

`never() -> Pattern`

Pattern method — call it on a pattern: `pattern.never(...)`.

Shorthand for `.sometimesBy(0, fn)` (never calls fn)

**Examples**

```js
s("hh*8").never(x=>x.speed("0.5"))
```

### noise

`noise(wet: Number|Pattern)`

Adds pink noise to the mix

**Parameters**

- `wet` (`Number|Pattern`): wet amount

**Examples**

```js
sound("<white pink brown>/2")
```

### note

`note(...)`

Requires arguments: at least 2.

Plays the given note name or midi number. A note name consists of

- a letter (a-g or A-G)
- optional accidentals (b or #)
- optional (possibly negative) octave number (0-9). Defaults to 3

Examples of valid note names: `c`, `bb`, `Bb`, `f#`, `c3`, `A4`, `Eb2`, `c#5`

You can also use midi numbers instead of note names, where 69 is mapped to A4 440Hz in 12EDO.

**Examples**

```js
note("c a f e")
```

```js
note("c4 a4 f4 e4")
```

```js
note("60 69 65 64")
```

```js
note("fbb1 a#0 cbbb-1 e##-2").sound("saw")
```

### nrpnn

`nrpnn(nrpnn: Number|Pattern)`

MIDI NRPN non-registered parameter number: Sends a MIDI NRPN non-registered parameter number message.

**Parameters**

- `nrpnn` (`Number|Pattern`): MIDI NRPN non-registered parameter number (0-127)

**Examples**

```js
note("c4").nrpnn("1:8").nrpv("123").midichan(1).midi()
```

### nrpv

`nrpv(nrpv: Number|Pattern)`

MIDI NRPN non-registered parameter value: Sends a MIDI NRPN non-registered parameter value message.

**Parameters**

- `nrpv` (`Number|Pattern`): MIDI NRPN non-registered parameter value (0-127)

**Examples**

```js
note("c4").nrpnn("1:8").nrpv("123").midichan(1).midi()
```

### octaves

`octaves(count: Number|Pattern)`

How many octaves are voicing steps spread apart, defaults to 1

**Parameters**

- `count` (`Number|Pattern`): the number of octaves

**Examples**

```js
chord("<Am C D F Am E Am E>").octaves("<2 4>").voicing()
```

### off

`off(time: Pattern|Number, func: Function) -> Pattern`

Pattern method — call it on a pattern: `pattern.off(...)`.

Superimposes the function result on top of the original pattern, delayed by the given time.

**Parameters**

- `time` (`Pattern|Number`): offset time
- `func` (`Function`): function to apply

**Examples**

```js
"c3 eb3 g3".off(1/8, x=>x.add(7)).note()
```

### offset

`offset(shift: Number|Pattern)`

Sets how the voicing is offset from the anchored position

**Parameters**

- `shift` (`Number|Pattern`): the amount to shift the voicing up or down

**Examples**

```js
chord("<Am C D F Am E Am E>").offset("<0 1 2 3 4 5>") // alter the voicing each time
```

### often

`often() -> Pattern`

Pattern method — call it on a pattern: `pattern.often(...)`.

Shorthand for `.sometimesBy(0.75, fn)`

**Examples**

```js
s("hh*8").often(x=>x.speed("0.5"))
```

### onsetsOnly

`onsetsOnly() -> Pattern`

Pattern method — call it on a pattern: `pattern.onsetsOnly(...)`.

Returns a new pattern, with all haps without onsets filtered out. A hap
with an onset is one with a `whole` timespan that begins at the same time
as its `part` timespan.

### onTriggerTime

`onTriggerTime() -> Pattern`

Pattern method — call it on a pattern: `pattern.onTriggerTime(...)`.

make something happen on event time
uses browser timeout which is innacurate for audio tasks

**Examples**

```js
s("bd!8").onTriggerTime((hap) => {console.log(hap)})
```

### orbit

`orbit(number: Number|Pattern)`

Synonyms: o

An `orbit` is a global parameter context for patterns. Patterns with the same orbit will share the same global effects.

**Parameters**

- `number` (`Number|Pattern`)

**Examples**

```js
stack(
  s("hh*6").delay(.5).delaytime(.25).orbit(1),
  s("~ sd ~ sd").delay(.5).delaytime(.125).orbit(2)
)
```

### oschost

`oschost(oschost: String|Pattern)`

The host to send open sound control messages to. Requires running the OSC bridge.

**Parameters**

- `oschost` (`String|Pattern`): e.g. 'localhost'

**Examples**

```js
note("c4").oschost('127.0.0.1').oscport(57120).osc();
```

### oscport

`oscport(oscport: Number|Pattern)`

The port to send open sound control messages to. Requires running the OSC bridge.

**Parameters**

- `oscport` (`Number|Pattern`): e.g. 57120

**Examples**

```js
note("c4").oschost('127.0.0.1').oscport(57120).osc();
```

### outside

`outside()`

Carries out an operation 'outside' a cycle.

**Examples**

```js
"<[0 1] 2 [3 4] 5>".outside(4, rev).scale('C major').note()
// "<[0 1] 2 [3 4] 5>".fast(4).rev().slow(4).scale('C major').note()
```

### pace

`pace()`

*Experimental*

Speeds a pattern up or down, to fit to the given number of steps per cycle.

**Examples**

```js
sound("bd sd cp").pace(4)
// The same as sound("{bd sd cp}%4") or sound("<bd sd cp>*4")
```

### palindrome

`palindrome()`

Applies `rev` to a pattern every other cycle, so that the pattern alternates between forwards and backwards.

**Examples**

```js
note("c d e g").palindrome()
```

### pan

`pan(pan: Number|Pattern)`

Sets position in stereo.

**Parameters**

- `pan` (`Number|Pattern`): between 0 and 1, from left to right (assuming stereo), once round a circle (assuming multichannel)

**Examples**

```js
s("[bd hh]*2").pan("<.5 1 .5 0>")
```

```js
s("bd rim sd rim bd ~ cp rim").pan(sine.slow(2))
```

### panchor

`panchor(anchor: Number|Pattern)`

Sets the range anchor of the envelope:

- anchor 0: range = [note, note + penv]
- anchor 1: range = [note - penv, note]
If you don't set an anchor, the value will default to the psustain value.

**Parameters**

- `anchor` (`Number|Pattern`): anchor offset

**Examples**

```js
note("c c4").penv(12).panchor("<0 .5 1 .5>")
```

### parray

`parray(...)`

Requires arguments: at least 1.

Turns a list of patterns into a single pattern which outputs list-values

### partials

`partials(magnitudes: Number[]|Pattern)`

Scale the magnitude of the harmonics of one of the core synths ('sine', 'tri', 'saw', ..)

Can also be used to create a new synth via `s('user').partials(...)`

**Parameters**

- `magnitudes` (`Number[]|Pattern`): List of [0, 1] magnitudes for partials. 0th entry is the fundamental harmonic (i.e. DC offset is skipped)

**Examples**

```js
s("user").seg(16).n(irand(8)).scale("A:major")
  .partials([1, 0, 1, 0, 0, 1])
```

```js
s("saw").seg(8).n(irand(12)).scale("G#:minor")
  .partials(binaryL(irand(256).add("1")))
```

### pattack

`pattack(time: Number|Pattern)`

Synonyms: patt

Attack time of pitch envelope.

**Parameters**

- `time` (`Number|Pattern`): time in seconds

**Examples**

```js
note("c eb g bb").pattack("0 .1 .25 .5").slow(2)
```

### Pattern

Create a pattern. As an end user, you will most likely not create a Pattern directly.

Class representing a pattern.

**Parameters**

- `query` (`Function`): The function that maps a `State` to an array of `Hap`.

### pcurve

`pcurve(type: Number|Pattern)`

Curve of envelope. Defaults to linear. exponential is good for kicks

**Parameters**

- `type` (`Number|Pattern`): 0 = linear, 1 = exponential

**Examples**

```js
note("g1*4")
.s("sine").pdec(.5)
.penv(32)
.pcurve("<0 1>")
```

### pdecay

`pdecay(time: Number|Pattern)`

Synonyms: pdec

Decay time of pitch envelope.

**Parameters**

- `time` (`Number|Pattern`): time in seconds

**Examples**

```js
note("<c eb g bb>").pdecay("<0 .1 .25 .5>")
```

### penv

`penv(semitones: Number|Pattern)`

Amount of pitch envelope. Negative values will flip the envelope.
If you don't set other pitch envelope controls, `pattack:.2` will be the default.

**Parameters**

- `semitones` (`Number|Pattern`): change in semitones

**Examples**

```js
note("c")
.penv("<12 7 1 .5 0 -1 -7 -12>")
```

### per

`per` — a Pattern value, not a function. Use it directly, e.g. `per.range(...)`.

Synonyms: perCycle

A pattern measuring the 'shortness' of events, or in other words, the duration of pattern events,
in events per cycle. `per` doesn't have structure itself, but takes structure, and therefore
event durations, from the pattern that it is combined with.
For example `per.struct("1 1 [1 1] 1")` would give the same as `"4 4 [8 8] 4"`.
See also its reciprocal, `cyclesPer`.

**Examples**

```js
// Shorter events are more distorted
n("0 0*2 0 0*2 0 [0 0 0]@2").sound("bd")
 .distort(per.div(2))
```

### perlin

`perlin` — a Pattern value, not a function. Use it directly, e.g. `perlin.range(...)`.

Generates a continuous pattern of perlin noise (https://en.wikipedia.org/wiki/Perlin_noise), in the range 0..1.

**Examples**

```js
// randomly change the cutoff
s("bd*4,hh*8").cutoff(perlin.range(500,8000))
```

### perx

`perx` — a Pattern value, not a function. Use it directly, e.g. `perx.range(...)`.

Like `per` but measures the shortness of events according to an exponential curve. In
particular, where the event duration halves, the
returned value increases by one. `perx.struct("1 1 [1 [1 1]] 1")` would therefore be
the same as `"3 3 [4 [5 5]] 3"`.

### phaser

`phaser(speed: Number|Pattern)`

Synonyms: ph

Phaser audio effect that approximates popular guitar pedals.

**Parameters**

- `speed` (`Number|Pattern`): speed of modulation

**Examples**

```js
n(run(8)).scale("D:pentatonic").s("sawtooth").release(0.5)
.phaser("<1 2 4 8>")
```

### phasercenter

`phasercenter(centerfrequency: Number|Pattern)`

Synonyms: phc

The center frequency of the phaser in HZ. Defaults to 1000

**Parameters**

- `centerfrequency` (`Number|Pattern`): in HZ

**Examples**

```js
n(run(8)).scale("D:pentatonic").s("sawtooth").release(0.5)
.phaser(2).phasercenter("<800 2000 4000>")
```

### phaserdepth

`phaserdepth(depth: Number|Pattern)`

Synonyms: phd, phasdp

The amount the signal is affected by the phaser effect. Defaults to 0.75

**Parameters**

- `depth` (`Number|Pattern`): number between 0 and 1

**Examples**

```js
n(run(8)).scale("D:pentatonic").s("sawtooth").release(0.5)
.phaser(2).phaserdepth("<0 .5 .75 1>")
```

### phasersweep

`phasersweep(phasersweep: Number|Pattern)`

Synonyms: phs

The frequency sweep range of the lfo for the phaser effect. Defaults to 2000

**Parameters**

- `phasersweep` (`Number|Pattern`): most useful values are between 0 and 4000

**Examples**

```js
n(run(8)).scale("D:pentatonic").s("sawtooth").release(0.5)
.phaser(2).phasersweep("<800 2000 4000>")
```

### phases

`phases(phases: Number[]|Pattern)`

Rotates the harmonics of one of the core synths ('sine', 'tri', 'saw', 'user', ..) by a list of phases

**Parameters**

- `phases` (`Number[]|Pattern`): List of [0, 1) phases for partials. 0th entry is the fundamental phase (i.e. DC offset is skipped)

**Examples**

```js
// Phase cancellation
s("saw").seg(8).n(irand(12)).scale("G#1:minor")
  .partials(partials([1, 1, 1]))
  .superimpose(x => x.phases([0.5, 0.5, 0.5]))
```

### pick

`pick(pat: Pattern, xs: *) -> Pattern`

Picks patterns (or plain values) either from a list (by index) or a lookup table (by name).
Similar to `inhabit`, but maintains the structure of the original patterns.

**Parameters**

- `pat` (`Pattern`)
- `xs` (`*`)

**Examples**

```js
note("<0 1 2!2 3>".pick(["g a", "e f", "f g f g" , "g c d"]))
```

```js
sound("<0 1 [2,0]>".pick(["bd sd", "cp cp", "hh hh"]))
```

```js
sound("<0!2 [0,1] 1>".pick(["bd(3,8)", "sd sd"]))
```

```js
s("<a!2 [a,b] b>".pick({a: "bd(3,8)", b: "sd sd"}))
```

### pickF

`pickF(pat: Pattern, lookup: Pattern, lookup: Function[]|Object) -> Pattern`

pickF lets you use a pattern of numbers to pick which function to apply to another pattern.

**Parameters**

- `pat` (`Pattern`)
- `lookup` (`Pattern`): a pattern of indices or names
- `lookup` (`Function[]|Object`): the array or lookup object of functions from which to pull

**Examples**

```js
s("bd [rim hh]").pickF("<0 1 2>", [rev,jux(rev),fast(2)])
```

```js
note("<c2 d2>(3,8)").s("square")
.pickF("<0 2> 1", [jux(rev), fast(2), x=>x.lpf(800)])
```

```js
note("<c2 d2>(3,8)").s("square")
.pickF("<jr l> f", { jr:jux(rev), f:fast(2), l:x=>x.lpf(800) })
```

### pickmod

`pickmod(pat: Pattern, xs: *) -> Pattern`

The same as `pick`, but if you pick a number greater than the size of the list,
it wraps around, rather than sticking at the maximum value.
For example, if you pick the fifth pattern of a list of three, you'll get the
second one.

**Parameters**

- `pat` (`Pattern`)
- `xs` (`*`)

### pickmodF

`pickmodF(pat: Pattern, lookup: Pattern, lookup: Function[]|Object) -> Pattern`

The same as `pickF`, but if you pick a number greater than the size of the functions list,
it wraps around, rather than sticking at the maximum value.

**Parameters**

- `pat` (`Pattern`)
- `lookup` (`Pattern`): a pattern of indices or names
- `lookup` (`Function[]|Object`): the array or lookup object of functions from which to pull

### pickmodOut

`pickmodOut(pat: Pattern, xs: *) -> Pattern`

The same as `pickOut`, but if you pick a number greater than the size of the list,
it wraps around, rather than sticking at the maximum value.

**Parameters**

- `pat` (`Pattern`)
- `xs` (`*`)

### pickmodReset

`pickmodReset(pat: Pattern, xs: *) -> Pattern`

The same as `pickReset`, but if you pick a number greater than the size of the list,
it wraps around, rather than sticking at the maximum value.

**Parameters**

- `pat` (`Pattern`)
- `xs` (`*`)

### pickmodRestart

`pickmodRestart(pat: Pattern, xs: *) -> Pattern`

The same as `pickRestart`, but if you pick a number greater than the size of the list,
it wraps around, rather than sticking at the maximum value.

**Parameters**

- `pat` (`Pattern`)
- `xs` (`*`)

**Examples**

```js
"<a@2 b@2 c@2 d@2>".pickRestart({
        a: n("0 1 2 0"),
        b: n("2 3 4 ~"),
        c: n("[4 5] [4 3] 2 0"),
        d: n("0 -3 0 ~")
      }).scale("C:major").s("piano")
```

### pickOut

`pickOut(pat: Pattern, xs: *) -> Pattern`

Similar to `pick`, but it applies an outerJoin instead of an innerJoin.

**Parameters**

- `pat` (`Pattern`)
- `xs` (`*`)

### pickReset

`pickReset(pat: Pattern, xs: *) -> Pattern`

Similar to `pick`, but the choosen pattern is reset when its index is triggered.

**Parameters**

- `pat` (`Pattern`)
- `xs` (`*`)

### pickRestart

`pickRestart(pat: Pattern, xs: *) -> Pattern`

Similar to `pick`, but the choosen pattern is restarted when its index is triggered.

**Parameters**

- `pat` (`Pattern`)
- `xs` (`*`)

### ply

`ply()`

The ply function repeats each event the given number of times.

**Examples**

```js
s("bd ~ sd cp").ply("<1 2 3>")
```

### plyForEach

`plyForEach(factor: Number, func: Function)`

Synonyms: plyforeach

The plyForEach function repeats each event the given number of times, applying the given function to each event.
This version of ply uses the iteration index as an argument to the function, similar to echoWith.

**Parameters**

- `factor` (`Number`): how many times to repeat
- `func` (`Function`): function to apply, given the pattern and the iteration index

**Examples**

```js
"<0 [2 4]>"
.plyForEach(4, (p,n) => p.add(n*2))
.scale("C:minor").note()
```

### plyWith

`plyWith(factor: Number, func: Function)`

Synonyms: plywith

The plyWith function repeats each event the given number of times, applying the given function to each event.\n

**Parameters**

- `factor` (`Number`): how many times to repeat
- `func` (`Function`): function to apply, given the pattern

**Examples**

```js
"<0 [2 4]>"
.plyWith(4, (p) => p.add(2))
.scale("C:minor").note()
```

### polymeter

`polymeter()`

Synonyms: pm

*Experimental*

Aligns the steps of the patterns, creating polymeters. The patterns are repeated until they all fit the cycle. For example, in the below the first pattern is repeated twice, and the second is repeated three times, to fit the lowest common multiple of six steps.

**Examples**

```js
// The same as note("{c eb g, c2 g2}%6")
polymeter("c eb g", "c2 g2").note()
```

### postgain

`postgain(...)`

Requires arguments: at least 2.

Gain applied after all effects have been processed.

**Examples**

```js
s("bd sd [~ bd] sd,hh*8")
.compressor("-20:20:10:.002:.02").postgain(1.5)
```

### prelease

`prelease(time: Number|Pattern)`

Synonyms: prel

Release time of pitch envelope

**Parameters**

- `time` (`Number|Pattern`): time in seconds

**Examples**

```js
note("<c eb g bb> ~")
.release(.5) // to hear the pitch release
.prelease("<0 .1 .25 .5>")
```

### press

`press()`

Syncopates a rhythm, by shifting each event halfway into its timespan.

**Examples**

```js
stack(s("hh*4"),
      s("bd mt sd ht").every(4, press)
     ).slow(2)
```

### pressBy

`pressBy()`

Like press, but allows you to specify the amount by which each
event is shifted. pressBy(0.5) is the same as press, while
pressBy(1/3) shifts each event by a third of its timespan.

**Examples**

```js
stack(s("hh*4"),
      s("bd mt sd ht").pressBy("<0 0.5 0.25>")
     ).slow(2)
```

### progNum

`progNum(program: Number|Pattern)`

MIDI program number: Sends a MIDI program change message.

**Parameters**

- `program` (`Number|Pattern`): MIDI program number (0-127)

**Examples**

```js
note("c4").progNum(10).midichan(1).midi()
```

### pure

`pure(...)`

Requires arguments: at least 1.

A discrete value that repeats once per cycle.

**Examples**

```js
pure('e4') // "e4"
```

### pw

`pw(pulsewidth: Number|Pattern)`

Controls the pulsewidth of the pulse oscillator

**Parameters**

- `pulsewidth` (`Number|Pattern`)

**Examples**

```js
note("{f a c e}%16").s("pulse").pw(".8:1:.2")
```

```js
n(run(8)).scale("D:pentatonic").s("pulse").pw("0 .75 .5 1")
```

### pwrate

`pwrate(rate: Number|Pattern)`

Synonyms: pwr

Controls the lfo rate for the pulsewidth of the pulse oscillator

**Parameters**

- `rate` (`Number|Pattern`)

**Examples**

```js
n(run(8)).scale("D:pentatonic").s("pulse").pw("0.5").pwrate("<5 .1 25>").pwsweep("<0.3 .8>")
```

### pwsweep

`pwsweep(sweep: Number|Pattern)`

Synonyms: pws

Controls the lfo sweep for the pulsewidth of the pulse oscillator

**Parameters**

- `sweep` (`Number|Pattern`)

**Examples**

```js
n(run(8)).scale("D:pentatonic").s("pulse").pw("0.5").pwrate("<5 .1 25>").pwsweep("<0.3 .8>")
```

### queryArc

`queryArc(begin: Fraction|Number, end: Fraction|Number) -> Hap[]`

Pattern method — call it on a pattern: `pattern.queryArc(...)`.

Query haps inside the given time span.

**Parameters**

- `begin` (`Fraction|Number`): from time
- `end` (`Fraction|Number`): to time

**Examples**

```js
const pattern = sequence('a', ['b', 'c'])
const haps = pattern.queryArc(0, 1)
console.log(haps)
silence
```

### rand

`rand` — a Pattern value, not a function. Use it directly, e.g. `rand.range(...)`.

A continuous pattern of random numbers, between 0 and 1.

**Examples**

```js
// randomly change the cutoff
s("bd*4,hh*8").cutoff(rand.range(500,8000))
```

### rand2

`rand2` — a Pattern value, not a function. Use it directly, e.g. `rand2.range(...)`.

A continuous pattern of random numbers, between -1 and 1

### randL

`randL(n: Number)`

Creates a list of random numbers of the given length

**Parameters**

- `n` (`Number`): Number of random numbers to sample

**Examples**

```js
s("saw").seg(16).n(irand(12)).scale("F1:minor")
  .partials(randL(8))
```

### range

`range() -> Pattern`

Pattern method — call it on a pattern: `pattern.range(...)`.

Assumes a numerical pattern, containing unipolar values in the range 0 .. 1.
Returns a new pattern with values scaled to the given min/max range.
Most useful in combination with continuous patterns.

**Examples**

```js
s("[bd sd]*2,hh*8")
.cutoff(sine.range(500,4000))
```

### range2

`range2() -> Pattern`

Pattern method — call it on a pattern: `pattern.range2(...)`.

Assumes a numerical pattern, containing bipolar values in the range -1 .. 1
Returns a new pattern with values scaled to the given min/max range.

**Examples**

```js
s("[bd sd]*2,hh*8")
.cutoff(sine2.range2(500,4000))
```

### rangex

`rangex() -> Pattern`

Pattern method — call it on a pattern: `pattern.rangex(...)`.

Assumes a numerical pattern, containing unipolar values in the range 0 .. 1
Returns a new pattern with values scaled to the given min/max range,
following an exponential curve.

**Examples**

```js
s("[bd sd]*2,hh*8")
.cutoff(sine.rangex(500,4000))
```

### rarely

`rarely() -> Pattern`

Pattern method — call it on a pattern: `pattern.rarely(...)`.

Shorthand for `.sometimesBy(0.25, fn)`

**Examples**

```js
s("hh*8").rarely(x=>x.speed("0.5"))
```

### ratio

`ratio() -> Pattern`

Pattern method — call it on a pattern: `pattern.ratio(...)`.

Allows dividing numbers via list notation using ":".
Returns a new pattern with just numbers.

**Examples**

```js
ratio("1, 5:4, 3:2").mul(110)
.freq().s("piano")
```

### ref

`ref(...)`

Requires arguments: at least 1.

exposes a custom value at query time. basically allows mutating state without evaluation

### register

`register(name: String|String[], func: Function, patternify: Boolean)`

Registers a new pattern method. The method is added to the Pattern class + the standalone function is returned from register.

**Parameters**

- `name` (`String|String[]`): name of the function, or an array of names to be used as synonyms
- `func` (`Function`): function with 1 or more params, where last is the current pattern
- `patternify` (`Boolean`): defaults to true; if set to false, you will have more control over the arguments to `func` as they will be in their raw form and it will be up to you to patternify them and/or query them for values

**Examples**

```js
const vlpf = register('vlpf', (freq, pat) => {
  return pat.fmap((v) => ({...v, cutoff: freq * (v.velocity ?? 1) }));
})
s("saw").seg(8).velocity(rand).vlpf(800)
```

### release

`release(time: Number|Pattern)`

Synonyms: rel

Amplitude envelope release time: The time it takes after the offset to go from sustain level to zero.

**Parameters**

- `time` (`Number|Pattern`): release time in seconds

**Examples**

```js
note("c3 e3 g3 c4").release("<0 .1 .4 .6 1>/2")
```

### removeUndefineds

`removeUndefineds(...)`

Pattern method — call it on a pattern: `pattern.removeUndefineds(...)`.

Requires arguments: at least 1.

Returns a new pattern, with haps containing undefined values removed from
query results.

### repeatCycles

`repeatCycles` — a Pattern value, not a function. Use it directly, e.g. `repeatCycles.range(...)`.

Pattern method — call it on a pattern: `pattern.repeatCycles(...)`.

Repeats each cycle the given number of times.

**Examples**

```js
note(irand(12).add(34)).segment(4).repeatCycles(2).s("gm_acoustic_guitar_nylon")
```

### replicate

`replicate()`

*Experimental*

`replicate` is similar to `fast` in that it increases its density, but it also increases the step count
accordingly. So `stepcat("a b".replicate(2), "c d")` would be the same as `"a b a b c d"`, whereas
`stepcat("a b".fast(2), "c d")` would be the same as `"[a b] [a b] c d"`.

TODO: find out how this function differs from extend

**Examples**

```js
stepcat(
  sound("bd bd - cp").replicate(2),
  sound("bd - sd -")
).pace(8)
```

### reset

`reset()`

Pattern method — call it on a pattern: `pattern.reset(...)`.

Resets the pattern to the start of the cycle for each onset of the reset pattern.

**Examples**

```js
s("[<bd lt> sd]*2, hh*8").reset("<x@3 x(5,8)>")
```

### restart

`restart()`

Pattern method — call it on a pattern: `pattern.restart(...)`.

Restarts the pattern for each onset of the restart pattern.
While reset will only reset the current cycle, restart will start from cycle 0.

**Examples**

```js
s("[<bd lt> sd]*2, hh*8").restart("<x@3 x(5,8)>")
```

### rev

`rev() -> Pattern`

Pattern method — call it on a pattern: `pattern.rev(...)`.

Reverse all cycles in a pattern. See also `revv` for reversing a whole pattern.

**Examples**

```js
note("c d e g").rev()
```

### revv

`revv() -> Pattern`

Pattern method — call it on a pattern: `pattern.revv(...)`.

Reverse a whole pattern. See also `rev` for reversing each cycle.

**Examples**

```js
// This is the same as `<[g e] [d c]>`. If `rev()` is used, you get
// the same as `<[d c] [g e]>`, where each cycle reverses, but the order of
// cycles stays the same.
note("<[c d] [e g]>").revv()
```

### ribbon

`ribbon(offset: Number, cycles: Number)`

Synonyms: rib

Loops the pattern inside an `offset` for `cycles`.
If you think of the entire span of time in cycles as a ribbon, you can cut a single piece and loop it.

**Parameters**

- `offset` (`Number`): start point of loop in cycles
- `cycles` (`Number`): loop length in cycles

**Examples**

```js
note("<c d e f>").ribbon(1, 2)
```

```js
// Looping a portion of randomness
n(irand(8).segment(4)).scale("c:pentatonic").ribbon(1337, 2)
```

```js
// rhythm generator
s("bd!16?").ribbon(29,.5)
```

### room

`room(level: Number|Pattern)`

Sets the level of reverb.

When using mininotation, you can also optionally add the 'size' parameter, separated by ':'.

**Parameters**

- `level` (`Number|Pattern`): between 0 and 1

**Examples**

```js
s("bd sd [~ bd] sd").room("<0 .2 .4 .6 .8 1>")
```

```js
s("bd sd [~ bd] sd").room("<0.9:1 0.9:4>")
```

### roomdim

`roomdim(frequency: Number)`

Synonyms: rdim

Reverb lowpass frequency at -60dB (in hertz).
When this property is changed, the reverb will be recaculated, so only change this sparsely..

**Parameters**

- `frequency` (`Number`): between 0 and 20000hz

**Examples**

```js
s("bd sd [~ bd] sd").room(0.5).rlp(10000).rdim(8000)
```

```js
s("bd sd [~ bd] sd").room(0.5).rlp(5000).rdim(400)
```

### roomfade

`roomfade(seconds: Number)`

Synonyms: rfade

Reverb fade time (in seconds).
When this property is changed, the reverb will be recaculated, so only change this sparsely..

**Parameters**

- `seconds` (`Number`): for the reverb to fade

**Examples**

```js
s("bd sd [~ bd] sd").room(0.5).rlp(10000).rfade(0.5)
```

```js
s("bd sd [~ bd] sd").room(0.5).rlp(5000).rfade(4)
```

### roomlp

`roomlp(frequency: Number)`

Synonyms: rlp

Reverb lowpass starting frequency (in hertz).
When this property is changed, the reverb will be recaculated, so only change this sparsely..

**Parameters**

- `frequency` (`Number`): between 0 and 20000hz

**Examples**

```js
s("bd sd [~ bd] sd").room(0.5).rlp(10000)
```

```js
s("bd sd [~ bd] sd").room(0.5).rlp(5000)
```

### roomsize

`roomsize(size: Number|Pattern)`

Synonyms: rsize, sz, size

Sets the room size of the reverb, see `room`.
When this property is changed, the reverb will be recaculated, so only change this sparsely..

**Parameters**

- `size` (`Number|Pattern`): between 0 and 10

**Examples**

```js
s("bd sd [~ bd] sd").room(.8).rsize(1)
```

```js
s("bd sd [~ bd] sd").room(.8).rsize(4)
```

### round

`round() -> Pattern`

Pattern method — call it on a pattern: `pattern.round(...)`.

Assumes a numerical pattern. Returns a new pattern with all values rounded
to the nearest integer.

**Examples**

```js
n("0.5 1.5 2.5".round()).scale("C:major")
```

### run

`run(...)`

Requires arguments: at least 1.

A discrete pattern of numbers from 0 to n-1

**Examples**

```js
n(run(4)).scale("C4:pentatonic")
// n("0 1 2 3").scale("C4:pentatonic")
```

### s

`s(sound: String|Pattern)`

Synonyms: sound

Select a sound / sample by name. When using mininotation, you can also optionally supply 'n' and 'gain' parameters
separated by ':'.

**Parameters**

- `sound` (`String|Pattern`): The sound / pattern of sounds to pick

**Examples**

```js
s("bd hh")
```

```js
s("bd:0 bd:1 bd:0:0.3 bd:1:1.4")
```

### saw

`saw` — a Pattern value, not a function. Use it directly, e.g. `saw.range(...)`.

A sawtooth signal between 0 and 1.

**Examples**

```js
note("<c3 [eb3,g3] g2 [g3,bb3]>*8")
.clip(saw.slow(2))
```

```js
n(saw.range(0,8).segment(8))
.scale('C major')
```

### saw2

`saw2` — a Pattern value, not a function. Use it directly, e.g. `saw2.range(...)`.

A sawtooth signal between -1 and 1 (like `saw`, but bipolar).

### scramble

`scramble()`

Slices a pattern into the given number of parts, then plays those parts at random. Similar to `shuffle`,
but parts might be played more than once, or not at all, per cycle.

**Examples**

```js
note("c d e f").sound("piano").scramble(4)
```

```js
seq("c d e f".scramble(4), "g").note().sound("piano")
```

### scrub

`scrub() -> Pattern`

Pattern method — call it on a pattern: `pattern.scrub(...)`.

Allows you to scrub an audio file like a tape loop by passing values that represents the position in the audio file
in the optional array syntax ex: "0.5:2", the second value controls the speed of playback

**Examples**

```js
samples('github:switchangel/pad')
s("swpad:0").scrub("{0.1!2 .25@3 0.7!2 <0.8:1.5>}%8")
```

```js
samples('github:yaxu/clean-breaks/main');
s("amen/4").fit().scrub("{0@3 0@2 4@3}%8".div(16))
```

### seed

`seed(n: Number)`

Change the seed for random signals. Normally, random signals depend on time,
so two patterns at the same time will have the same random values. Specifying
a new seed changes the signal output by `rand`. This also affects other functions
that use randomness, like `shuffle` and `sometimes`.

**Parameters**

- `n` (`Number`): A new seed. Can be any number.

**Examples**

```js
$: s("hh*4").degrade();
$: s("bd*4").degrade().seed(1); // Will degrade different events from the hi-hat
```

### segment

`segment(segments: Number)`

Synonyms: seg

Samples the pattern at a rate of n events per cycle. Useful for turning a continuous pattern into a discrete one.

**Parameters**

- `segments` (`Number`): number of segments per cycle

**Examples**

```js
note(saw.range(40,52).segment(24))
```

### seq

`seq()`

Synonyms: fastcat

Like **cat**, but the items are crammed into one cycle.

**Examples**

```js
seq("e5", "b4", ["d5", "c5"]).note()
// "e5 b4 [d5 c5]".note()
```

```js
// As a chained function:
s("hh*4").seq(
  note("c4(5,8)")
)
```

### seqPLoop

`seqPLoop() -> Pattern`

Similarly to `arrange`, allows you to arrange multiple patterns together over multiple cycles.
Unlike `arrange`, you specify a start and stop time for each pattern rather than duration, which
means that patterns can overlap.

**Examples**

```js
seqPLoop(
  [0, 2, "bd(3,8)"],
  [1, 3, "cp(3,8)"]
).sound()
```

### sequence

`sequence()`

See `fastcat`

### sequenceP

`sequenceP(...)`

Requires arguments: at least 1.

Takes a list of patterns, and returns a pattern of lists.

### set

`set(pat: Pattern) -> Pattern`

Pattern method — call it on a pattern: `pattern.set(...)`.

When called on a pattern `a`, with a input pattern `b` (`a.set(b)`),
combines `a` and `b` such that anything defined in `b`
and anything defined in `a` that is *not* defined in `b`
will be in the resulting pattern.

The structure is maintained from `a`,
because the default pattern alignment is `in`,
see the section on `Pattern Alignment`
in the technical manual in the docs

This is the inverse of `keep`

See examples below

**Parameters**

- `pat` (`Pattern`)

**Examples**

```js
// because input pattern has `s` set,
// it overrides the "sine" declared earlier
note("c a f e").s("sine").set(s("triangle"))
```

### setContext

`setContext(context: *) -> Pattern`

Pattern method — call it on a pattern: `pattern.setContext(...)`.

Returns a new pattern with the context field set to every hap set to the given value.

**Parameters**

- `context` (`*`)

### setcpm

`setcpm(cpm: Number)`

Changes the global tempo to the given cycles per minute

**Parameters**

- `cpm` (`Number`): cycles per minute

**Examples**

```js
setcpm(140/4) // =140 bpm in 4/4
$: s("bd*4,[- sd]*2").bank('tr707')
```

### setDefaultJoin

`setDefaultJoin(method: String)`

Sets the default method of combining events from two patterns (aka alignment (https://strudel.cc/technical-manual/alignment/)) in Strudel.
The default method is 'in', meaning that patterns to the left will (typically) dictate the event timings when combined with patterns to the right.
By changing alignment to 'out', the opposite will happen. With 'mix', they will combine their event timings.

Note that we say the *default* method, because alignments can also be set explicitly with calls like
'add.mix', 'set.squeeze', etc.

**Parameters**

- `method` (`String`): Default join method to use. Options: 'in', 'out', 'mix', 'squeeze', 'squeezeout', 'reset', 'restart', 'poly'

**Examples**

```js
setDefaultJoin('mix') // also try 'in', 'out', 'squeeze', etc.
s("saw").vel("1 0.5").note("F A C E").delay("0 0.2 0.3")
```

### shape

`shape(distortion: Number|Pattern)`

(Deprecated) Wave shaping distortion. WARNING: can suddenly get unpredictably loud.
Please use distort instead, which has a more predictable response curve
second option in optional array syntax (ex: ".9:.5") applies a postgain to the output

**Parameters**

- `distortion` (`Number|Pattern`): between 0 and 1

**Examples**

```js
s("bd sd [~ bd] sd,hh*8").shape("<0 .2 .4 .6 .8>")
```

### showFirstCycle

`showFirstCycle`

Read it from a pattern: `pattern.showFirstCycle` (an accessor, not a method — do not call it).

More human-readable version of the `firstCycleValues` accessor.

### shrink

`shrink() -> Pattern`

*Experimental*

Progressively shrinks the pattern by 'n' steps until there's nothing left, or if a second value is given (using mininotation list syntax with `:`),
that number of times.
A positive number will progressively drop steps from the start of a pattern, and a negative number from the end.

**Examples**

```js
"tha dhi thom nam".shrink("1").sound()
.bank("mridangam")
```

```js
"tha dhi thom nam".shrink("-1").sound()
.bank("mridangam")
```

```js
"tha dhi thom nam".shrink("1 -1").sound().bank("mridangam").pace(4)
```

```js
note("0 1 2 3 4 5 6 7".scale("C:ritusen")).sound("folkharp")
   .shrink("1 -1").pace(8)
```

### shuffle

`shuffle()`

Slices a pattern into the given number of parts, then plays those parts in random order.
Each part will be played exactly once per cycle.

**Examples**

```js
note("c d e f").sound("piano").shuffle(4)
```

```js
seq("c d e f".shuffle(4), "g").note().sound("piano")
```

### silence

`silence` — a Pattern value, not a function. Use it directly, e.g. `silence.range(...)`.

Does absolutely nothing..

**Examples**

```js
silence // "~"
```

### sine

`sine` — a Pattern value, not a function. Use it directly, e.g. `sine.range(...)`.

A sine signal between 0 and 1.

**Examples**

```js
n(sine.segment(16).range(0,15))
.scale("C:minor")
```

### sine2

`sine2` — a Pattern value, not a function. Use it directly, e.g. `sine2.range(...)`.

A sine signal between -1 and 1 (like `sine`, but bipolar).

### sinefold

`sinefold(distortion: Number|Pattern, volume: Number|Pattern)`

Wavefolding distortion composed with sinusoid

**Parameters**

- `distortion` (`Number|Pattern`): amount of distortion to apply
- `volume` (`Number|Pattern`): linear postgain of the distortion

### slice

`slice() -> Pattern`

Pattern method — call it on a pattern: `pattern.slice(...)`.

Chops samples into the given number of slices, triggering those slices with a given pattern of slice numbers.
Instead of a number, it also accepts a list of numbers from 0 to 1 to slice at specific points.

**Examples**

```js
samples('github:tidalcycles/dirt-samples')
s("breaks165").slice(8, "0 1 <2 2*2> 3 [4 0] 5 6 7".every(3, rev)).slow(0.75)
```

```js
samples('github:tidalcycles/dirt-samples')
s("breaks125").fit().slice([0,.25,.5,.75], "0 1 1 <2 3>")
```

### slow

`slow(factor: Number|Pattern) -> Pattern`

Pattern method — call it on a pattern: `pattern.slow(...)`.

Synonyms: sparsity

Slow down a pattern over the given number of cycles. Like the "/" operator in mini notation.

**Parameters**

- `factor` (`Number|Pattern`): slow down factor

**Examples**

```js
s("bd hh sd hh").slow(2) // s("[bd hh sd hh]/2")
```

### slowcat

`slowcat() -> Pattern`

Synonyms: cat

Concatenation: combines a list of patterns, switching between them successively, one per cycle.

**Examples**

```js
slowcat("e5", "b4", ["d5", "c5"])
```

### slowcatPrime

`slowcatPrime(...items: any) -> Pattern`

Concatenation: combines a list of patterns, switching between them successively, one per cycle. Unlike slowcat, this version will skip cycles.

**Parameters**

- `...items` (`any`): The items to concatenate

### soft

`soft(distortion: Number|Pattern, volume: Number|Pattern)`

Soft-clipping distortion

**Parameters**

- `distortion` (`Number|Pattern`): amount of distortion to apply
- `volume` (`Number|Pattern`): linear postgain of the distortion

### someCycles

`someCycles() -> Pattern`

Pattern method — call it on a pattern: `pattern.someCycles(...)`.

Shorthand for `.someCyclesBy(0.5, fn)`

**Examples**

```js
s("bd,hh*8").someCycles(x=>x.speed("0.5"))
```

### someCyclesBy

`someCyclesBy(probability: Number|Pattern, function: Function) -> Pattern`

Pattern method — call it on a pattern: `pattern.someCyclesBy(...)`.

Randomly applies the given function by the given probability on a cycle by cycle basis.
Similar to `sometimesBy`

**Parameters**

- `probability` (`Number|Pattern`): a number between 0 and 1
- `function` (`Function`): the transformation to apply

**Examples**

```js
s("bd,hh*8").someCyclesBy(.3, x=>x.speed("0.5"))
```

### sometimes

`sometimes(function: Function) -> Pattern`

Pattern method — call it on a pattern: `pattern.sometimes(...)`.

Applies the given function with a 50% chance

**Parameters**

- `function` (`Function`): the transformation to apply

**Examples**

```js
s("hh*8").sometimes(x=>x.speed("0.5"))
```

### sometimesBy

`sometimesBy(probability: Number|Pattern, function: Function) -> Pattern`

Pattern method — call it on a pattern: `pattern.sometimesBy(...)`.

Randomly applies the given function by the given probability.
Similar to `someCyclesBy`

**Parameters**

- `probability` (`Number|Pattern`): a number between 0 and 1
- `function` (`Function`): the transformation to apply

**Examples**

```js
s("hh*8").sometimesBy(.4, x=>x.speed("0.5"))
```

### sortHapsByPart

`sortHapsByPart() -> Pattern`

Pattern method — call it on a pattern: `pattern.sortHapsByPart(...)`.

Returns a new pattern, which returns haps sorted in temporal order. Mainly
of use when comparing two patterns for equality, in tests.

### source

`source(getSource: Function)`

Synonyms: src

Define a custom webaudio node to use as a sound source.

**Parameters**

- `getSource` (`Function`)

### speed

`speed(speed: Number|Pattern)`

Changes the speed of sample playback, i.e. a cheap way of changing pitch.

**Parameters**

- `speed` (`Number|Pattern`): inf to inf, negative numbers play the sample backwards.

**Examples**

```js
s("bd*6").speed("1 2 4 1 -2 -4")
```

```js
speed("1 1.5*2 [2 1.1]").s("piano").clip(1)
```

### splice

`splice()`

Works the same as slice, but changes the playback speed of each slice to match the duration of its step.

**Examples**

```js
samples('github:tidalcycles/dirt-samples')
s("breaks165")
.splice(8,  "0 1 [2 3 0]@2 3 0@2 7")
```

### splitQueries

`splitQueries() -> Pattern`

Pattern method — call it on a pattern: `pattern.splitQueries(...)`.

Returns a new pattern, with queries split at cycle boundaries. This makes
some calculations easier to express, as all haps are then constrained to
happen within a cycle.

### spread

`spread(spread: Number|Pattern)`

Set the stereo pan spread for supported oscillators

**Parameters**

- `spread` (`Number|Pattern`): between 0 and 1

**Examples**

```js
note("d f a a# a d3").fast(2).s("supersaw").spread("<0 .3 1>")
```

### square

`square` — a Pattern value, not a function. Use it directly, e.g. `square.range(...)`.

A square signal between 0 and 1.

**Examples**

```js
n(square.segment(4).range(0,7)).scale("C:minor")
```

### square2

`square2` — a Pattern value, not a function. Use it directly, e.g. `square2.range(...)`.

A square signal between -1 and 1 (like `square`, but bipolar).

### squeeze

`squeeze(pat: Pattern, xs: *) -> Pattern`

Pick from the list of values (or patterns of values) via the index using the given
pattern of integers. The selected pattern will be compressed to fit the duration of the selecting event

**Parameters**

- `pat` (`Pattern`)
- `xs` (`*`)

**Examples**

```js
note(squeeze("<0@2 [1!2] 2>", ["g a", "f g f g" , "g a c d"]))
```

### stack

`stack() -> Pattern`

Synonyms: polyrhythm, pr

The given items are played at the same time at the same length.

**Examples**

```js
stack("g3", "b3", ["e4", "d4"]).note()
// "g3,b3,[e4 d4]".note()
```

```js
// As a chained function:
s("hh*4").stack(
  note("c4(5,8)")
)
```

### stepalt

`stepalt() -> Pattern`

*Experimental*

Concatenates patterns stepwise, according to an inferred 'steps per cycle'.
Similar to `stepcat`, but if an argument is a list, the whole pattern will alternate between the elements in the list.

**Examples**

```js
stepalt(["bd cp", "mt"], "bd").sound()
// The same as "bd cp bd mt bd".sound()
```

### stepcat

`stepcat() -> Pattern`

Synonyms: timeCat, timecat

'Concatenates' patterns like `fastcat`, but proportional to a number of steps per cycle.
The steps can either be inferred from the pattern, or provided as a [length, pattern] pair.
Has the alias `timecat`.

**Examples**

```js
stepcat([3,"e3"],[1, "g3"]).note()
// the same as "e3@3 g3".note()
```

```js
stepcat("bd sd cp","hh hh").sound()
// the same as "bd sd cp hh hh".sound()
```

### stretch

`stretch(factor: Number|Pattern)`

Changes the pitch of the sample without changing its speed.
The frequencies are multiplied by (factor + 1) for positive numbers
and by max(factor / 4 + 1, 0) for negative numbers.
So tuning up by octaves can be done with 1, 3, 7, ...
and tuning down by octaves with -2, -3, -3.5...

**Parameters**

- `factor` (`Number|Pattern`): between `-4` and `inf`. Positive increases pitch, 0 does nothing, negative decreases the pitch.

**Examples**

```js
s("gm_flute").stretch("<2 1 0 -2>")
```

### striate

`striate() -> Pattern`

Pattern method — call it on a pattern: `pattern.striate(...)`.

Cuts each sample into the given number of parts, triggering progressive portions of each sample at each loop.

**Examples**

```js
s("numbers:0 numbers:1 numbers:2").striate(6).slow(3)
```

### stripContext

`stripContext() -> Pattern`

Pattern method — call it on a pattern: `pattern.stripContext(...)`.

Returns a new pattern with the context field of every hap set to an empty object.

### struct

`struct()`

Pattern method — call it on a pattern: `pattern.struct(...)`.

Applies the given structure to the pattern:

**Examples**

```js
note("c,eb,g")
  .struct("x ~ x ~ ~ x ~ x ~ ~ ~ x ~ x ~ ~")
  .slow(2)
```

### stut

`stut(times: Number, feedback: Number, time: Number)`

Deprecated. Like echo, but the last 2 parameters are flipped.

**Parameters**

- `times` (`Number`): how many times to repeat
- `feedback` (`Number`): velocity multiplicator for each iteration
- `time` (`Number`): cycle offset between iterations

**Examples**

```js
s("bd sd").stut(3, .8, 1/6)
```

### sub

`sub()`

Pattern method — call it on a pattern: `pattern.sub(...)`.

Like add, but the given numbers are subtracted.

**Examples**

```js
n("0 2 4".sub("<0 1 2 3>")).scale("C4:minor")
// See add for more information.
```

### superimpose

`superimpose() -> Pattern`

Pattern method — call it on a pattern: `pattern.superimpose(...)`.

Superimposes the result of the given function(s) on top of the original pattern:

**Examples**

```js
"<0 2 4 6 ~ 4 ~ 2 0!3 ~!5>*8"
  .superimpose(x=>x.add(2))
  .scale('C minor').note()
```

### sustain

`sustain(gain: Number|Pattern)`

Synonyms: sus

Amplitude envelope sustain level: The level which is reached after attack / decay, being sustained until the offset.

**Parameters**

- `gain` (`Number|Pattern`): sustain level between 0 and 1

**Examples**

```js
note("c3 e3 f3 g3").decay(.2).sustain("<0 .1 .4 .6 1>")
```

### swing

`swing(subdivision: Number)`

Shorthand for swingBy with 1/3:

**Parameters**

- `subdivision` (`Number`)

**Examples**

```js
s("hh*8").swing(4)
// s("hh*8").swingBy(1/3, 4)
```

### swingBy

`swingBy(subdivision: Number, offset: Number)`

The function `swingBy x n` breaks each cycle into `n` slices, and then delays events in the second half of each slice by the amount `x`, which is relative to the size of the (half) slice. So if `x` is 0 it does nothing, `0.5` delays for half the note duration, and 1 will wrap around to doing nothing again. The end result is a shuffle or swing-like rhythm

**Parameters**

- `subdivision` (`Number`)
- `offset` (`Number`)

**Examples**

```js
s("hh*8").swingBy(1/3, 4)
```

### sysex

`sysex(id: Number|Pattern, data: Number|Pattern)`

MIDI sysex: Sends a MIDI sysex message.

**Parameters**

- `id` (`Number|Pattern`): Sysex ID
- `data` (`Number|Pattern`): Sysex data

**Examples**

```js
note("c4").sysex(["0x77", "0x01:0x02:0x03:0x04"]).midichan(1).midi()
```

### sysexdata

`sysexdata(data: Number|Pattern)`

MIDI sysex data: Sends a MIDI sysex message.

**Parameters**

- `data` (`Number|Pattern`): Sysex data

**Examples**

```js
note("c4").sysexid("0x77").sysexdata("0x01:0x02:0x03:0x04").midichan(1).midi()
```

### sysexid

`sysexid(id: Number|Pattern)`

MIDI sysex ID: Sends a MIDI sysex identifier message.

**Parameters**

- `id` (`Number|Pattern`): Sysex ID

**Examples**

```js
note("c4").sysexid("0x77").sysexdata("0x01:0x02:0x03:0x04").midichan(1).midi()
```

### tag

`tag(tag: String)`

Tags each Hap with an identifier. Good for filtering. The function populates Hap.context.tags (Array).

**Parameters**

- `tag` (`String`): anything unique

**Examples**

```js
s("saw!16").note("F1")
  .lpf(tri.range(40, 80).slow(4)).lpenv(5).lpq(4).lpd(0.15)
  .when(rand.late(0.1).gte(0.5), x => x.transpose("12").tag('altered'))
  .when(rand.late(0.2).gte(0.5), x => x.s("square").tag('altered'))
  .when("<0 1>", x => x.filter((hap) => hap.hasTag('altered')))
```

### take

`take() -> Pattern`

*Experimental*

Takes the given number of steps from a pattern (dropping the rest).
A positive number will take steps from the start of a pattern, and a negative number from the end.

**Examples**

```js
"bd cp ht mt".take("2").sound()
// The same as "bd cp".sound()
```

```js
"bd cp ht mt".take("1 2 3").sound()
// The same as "bd bd cp bd cp ht".sound()
```

```js
"bd cp ht mt".take("-1 -2 -3").sound()
// The same as "mt ht mt cp ht mt".sound()
```

### time

`time` — a Pattern value, not a function. Use it directly, e.g. `time.range(...)`.

A signal representing the cycle time.

### timecat

`timecat()`

Aliases for `stepcat`

### toBipolar

`toBipolar() -> Pattern`

Assumes a numerical pattern, containing unipolar values in the range 0 ..

1. Returns a new pattern with values scaled to the bipolar range -1 .. 1

### tour

`tour(...)`

Requires arguments: at least 1.

*Experimental*

Inserts a pattern into a list of patterns. On the first repetition it will be inserted at the end of the list, then moved backwards through the list
on successive repetitions. The patterns are added together stepwise, with all repetitions taking place over a single cycle. Using `pace` to set the
number of steps per cycle is therefore usually recommended.

**Examples**

```js
"[c g]".tour("e f", "e f g", "g f e c").note()
   .sound("folkharp")
   .pace(8)
```

### transient

`transient(attack: Number|Pattern, sustain: Number|Pattern)`

Transient shaper. Gives independent control over the emphasis on transients
and sustains

**Parameters**

- `attack` (`Number|Pattern`): Emphasis on transients; between -1 (deaccentuate) and 1 (accentuate)
- `sustain` (`Number|Pattern`): Emphasis on the sustains; between -1 (deaccentuate) and 1 (accentuate)

**Examples**

```js
s("bd").transient("<-1 -0.5 0 0.5 1>")
```

```js
s("hh*16").bank("tr909").transient("<-1:1 1:-1>")
```

### tremolo

`tremolo(speed: Number|Pattern)`

Synonyms: trem

Modulate the amplitude of a sound with a continuous waveform

**Parameters**

- `speed` (`Number|Pattern`): modulation speed in HZ

**Examples**

```js
note("d d d# d".fast(4)).s("supersaw").tremolo("<3 2 100> ").tremoloskew("<.5>")
```

### tremolodepth

`tremolodepth(depth: Number|Pattern)`

Synonyms: tremdepth

Depth of amplitude modulation

**Parameters**

- `depth` (`Number|Pattern`)

**Examples**

```js
note("a1 a1 a#1 a1".fast(4)).s("pulse").tremsync(4).tremolodepth("<1 2 .7>")
```

### tremolophase

`tremolophase(offset: Number|Pattern)`

Synonyms: tremphase

Alter the phase of the modulation waveform

**Parameters**

- `offset` (`Number|Pattern`): the offset in cycles of the modulation

**Examples**

```js
note("{f a c e}%16").s("sawtooth").tremsync(4).tremolophase("<0 .25 .66>")
```

### tremoloshape

`tremoloshape(shape: Number|Pattern)`

Synonyms: tremshape

Shape of amplitude modulation

**Parameters**

- `shape` (`Number|Pattern`): tri | square | sine | saw | ramp

**Examples**

```js
note("{f g c d}%16").tremsync(4).tremoloshape("<sine tri square>").s("sawtooth")
```

### tremoloskew

`tremoloskew(amount: Number|Pattern)`

Synonyms: tremskew

Alter the shape of the modulation waveform

**Parameters**

- `amount` (`Number|Pattern`): between 0 & 1, the shape of the waveform

**Examples**

```js
note("{f a c e}%16").s("sawtooth").tremsync(4).tremoloskew("<.5 0 1>")
```

### tremolosync

`tremolosync(cycles: Number|Pattern)`

Synonyms: tremsync

Modulate the amplitude of a sound with a continuous waveform

**Parameters**

- `cycles` (`Number|Pattern`): modulation speed in cycles

**Examples**

```js
note("d d d# d".fast(4)).s("supersaw").tremolosync("4").tremoloskew("<1 .5 0>")
```

### tri

`tri` — a Pattern value, not a function. Use it directly, e.g. `tri.range(...)`.

A triangle signal between 0 and 1.

**Examples**

```js
n(tri.segment(8).range(0,7)).scale("C:minor")
```

### tri2

`tri2` — a Pattern value, not a function. Use it directly, e.g. `tri2.range(...)`.

A triangle signal between -1 and 1 (like `tri`, but bipolar).

### undegrade

`undegrade() -> Pattern`

Pattern method — call it on a pattern: `pattern.undegrade(...)`.

Inverse of `degrade`: Randomly removes 50% of events from the pattern. Shorthand for `.undegradeBy(0.5)`
Events that would be removed by degrade are let through by undegrade and vice versa (see second example).

**Examples**

```js
s("hh*8").undegrade()
```

```js
s("hh*10").layer(
  x => x.degrade().pan(0),
  x => x.undegrade().pan(1)
)
```

### undegradeBy

`undegradeBy(amount: Number) -> Pattern`

Pattern method — call it on a pattern: `pattern.undegradeBy(...)`.

Inverse of `degradeBy`: Randomly removes events from the pattern by a given amount.
0 = 100% chance of removal
1 = 0% chance of removal
Events that would be removed by degradeBy are let through by undegradeBy and vice versa (see second example).

**Parameters**

- `amount` (`Number`): a number between 0 and 1

**Examples**

```js
s("hh*8").undegradeBy(0.2)
```

```js
s("hh*10").layer(
  x => x.degradeBy(0.2).pan(0),
  x => x.undegradeBy(0.8).pan(1)
)
```

### unison

`unison(numvoices: Number|Pattern)`

Set number of stacked voices for supported oscillators.

**Parameters**

- `numvoices` (`Number|Pattern`)

**Examples**

```js
note("d f a a# a d3").fast(2).s("supersaw").unison("<1 2 7>")
```

### useRNG

`useRNG(mod: String)`

Sets which random number generator to use. Historically Strudel would
use `useRNG('legacy')`, which remains the default. To use a new more statistically
precise RNG, try `useRNG('precise')`.

**Parameters**

- `mod` (`String`): Mode. One of 'legacy', 'precise'

**Examples**

```js
useRNG('legacy')
// Repeats every 300 cycles
$: n(irand(50)).seg(16).scale("C:minor").ribbon(88, 32)
$: n(irand(50)).seg(16).scale("C:minor").ribbon(388, 32)
```

### velocity

`velocity(...)`

Requires arguments: at least 2.

Synonyms: vel

Sets the velocity from 0 to 1. Is multiplied together with gain.

**Examples**

```js
s("hh*8")
.gain(".4!2 1 .4!2 1 .4 1")
.velocity(".4 1")
```

### vib

`vib(frequency: Number|Pattern)`

Synonyms: vibrato, v

Applies a vibrato to the frequency of the oscillator.

**Parameters**

- `frequency` (`Number|Pattern`): of the vibrato in hertz

**Examples**

```js
note("a e")
.vib("<.5 1 2 4 8 16>")
._scope()
```

```js
// change the modulation depth with ":"
note("a e")
.vib("<.5 1 2 4 8 16>:12")
._scope()
```

### vibmod

`vibmod(depth: Number|Pattern)`

Synonyms: vmod

Sets the vibrato depth in semitones. Only has an effect if `vibrato` | `vib` | `v` is is also set

**Parameters**

- `depth` (`Number|Pattern`): of vibrato (in semitones)

**Examples**

```js
note("a e").vib(4)
.vibmod("<.25 .5 1 2 12>")
._scope()
```

```js
// change the vibrato frequency with ":"
note("a e")
.vibmod("<.25 .5 1 2 12>:8")
._scope()
```

### vowel

`vowel(vowel: String|Pattern)`

Formant filter to make things sound like vowels.

**Parameters**

- `vowel` (`String|Pattern`): You can use a e i o u ae aa oe ue y uh un en an on, corresponding to [a] [e] [i] [o] [u] [æ] [ɑ] [ø] [y] [ɯ] [ʌ] [œ̃] [ɛ̃] [ɑ̃] [ɔ̃]. Aliases: aa = å = ɑ, oe = ø = ö, y = ı, ae = æ.

**Examples**

```js
note("[c2 <eb2 <g2 g1>>]*2").s('sawtooth')
.vowel("<a e i <o u>>")
```

```js
s("bd sd mt ht bd [~ cp] ht lt").vowel("[a|e|i|o|u]")
```

### warp

`warp(amount: Number|Pattern)`

Synonyms: wavetableWarp

Amount of warp (alteration of the waveform) to apply to the wavetable oscillator

**Parameters**

- `amount` (`Number|Pattern`): Warp of the wavetable from 0 to 1

**Examples**

```js
s("basique").bank("wt_digital").seg(8).note("F1").warp("0 0.25 0.5 0.75 1")
  .warpmode("spin")
```

### warpattack

`warpattack(time: Number|Pattern)`

Synonyms: warpatt

Attack time of the wavetable oscillator's warp envelope

**Parameters**

- `time` (`Number|Pattern`): attack time in seconds

### warpdc

`warpdc(dcoffset: Number|Pattern)`

DC offset of the LFO for the wavetable oscillator's warp

**Parameters**

- `dcoffset` (`Number|Pattern`): dc offset. set to 0 for unipolar

### warpdecay

`warpdecay(time: Number|Pattern)`

Synonyms: warpdec

Decay time of the wavetable oscillator's warp envelope

**Parameters**

- `time` (`Number|Pattern`): decay time in seconds

### warpdepth

`warpdepth(depth: Number|Pattern)`

Depth of the LFO for the wavetable oscillator's warp

**Parameters**

- `depth` (`Number|Pattern`): depth of modulation

### warpenv

`warpenv(amount: Number|Pattern)`

Amount of envelope applied wavetable oscillator's position envelope

**Parameters**

- `amount` (`Number|Pattern`): between 0 and 1

### warpmode

`warpmode(mode: Number|String|Pattern)`

Synonyms: wavetableWarpMode

Type of warp (alteration of the waveform) to apply to the wavetable oscillator.

The current options are: none, asym, bendp, bendm, bendmp, sync, quant, fold, pwm, orbit,
spin, chaos, primes, binary, brownian, reciprocal, wormhole, logistic, sigmoid, fractal, flip

**Parameters**

- `mode` (`Number|String|Pattern`): Warp mode

**Examples**

```js
s("morgana").bank("wt_digital").seg(8).note("F1").warp("0 0.25 0.5 0.75 1")
  .warpmode("<asym bendp spin logistic sync wormhole brownian>*2")
```

### warprate

`warprate(rate: Number|Pattern)`

Rate of the LFO for the wavetable oscillator's warp

**Parameters**

- `rate` (`Number|Pattern`): rate in hertz

### warprelease

`warprelease(time: Number|Pattern)`

Synonyms: warprel

Release time of the wavetable oscillator's warp envelope

**Parameters**

- `time` (`Number|Pattern`): release time in seconds

### warpshape

`warpshape(shape: Number|Pattern)`

Shape of the LFO for the wavetable oscillator's warp

**Parameters**

- `shape` (`Number|Pattern`): Shape of the lfo (0, 1, 2,..)

### warpskew

`warpskew(skew: Number|Pattern)`

Skew of the LFO for the wavetable oscillator's warp

**Parameters**

- `skew` (`Number|Pattern`): How much to bend the LFO shape

### warpsustain

`warpsustain(gain: Number|Pattern)`

Synonyms: warpsus

Sustain time of the wavetable oscillator's warp envelope

**Parameters**

- `gain` (`Number|Pattern`): sustain level (0 to 1)

### warpsync

`warpsync(rate: Number|Pattern)`

cycle synced rate of the LFO for the wavetable warp position

**Parameters**

- `rate` (`Number|Pattern`): rate in cycles

### wchoose

`wchoose(...pairs: any) -> Pattern`

Chooses randomly from the given list of elements by giving a probability to each element

**Parameters**

- `...pairs` (`any`): arrays of value and weight

**Returns** `Pattern` — a continuous pattern.

**Examples**

```js
note("c2 g2!2 d2 f1").s(wchoose(["sine",10], ["triangle",1], ["bd:6",1]))
```

### wchooseCycles

`wchooseCycles() -> Pattern`

Synonyms: wrandcat

Picks one of the elements at random each cycle by giving a probability to each element

**Examples**

```js
wchooseCycles(["bd",10], ["hh",1], ["sd",1]).s().fast(8)
```

```js
wchooseCycles(["c c c",5], ["a a a",3], ["f f f",1]).fast(4).note()
```

```js
// The probability can itself be a pattern
wchooseCycles(["bd(3,8)","<5 0>"], ["hh hh hh",3]).fast(4).s()
```

### when

`when(binary_pat: Pattern, func: Function) -> Pattern`

Pattern method — call it on a pattern: `pattern.when(...)`.

Applies the given function whenever the given pattern is in a true state.

**Parameters**

- `binary_pat` (`Pattern`)
- `func` (`Function`)

**Examples**

```js
"c3 eb3 g3".when("<0 1>/2", x=>x.sub("5")).note()
```

### whenKey

`whenKey() -> Pattern`

Pattern method — call it on a pattern: `pattern.whenKey(...)`.

Do something on a keypress, or array of keypresses
Key name reference (https://developer.mozilla.org/en-US/docs/Web/API/UI_Events/Keyboard_event_key_values)

**Examples**

```js
s("bd(5,8)").whenKey("Control:j", x => x.segment(16).color("red")).whenKey("Control:i", x => x.fast(2).color("blue"))
```

### withContext

`withContext(func: Function) -> Pattern`

Pattern method — call it on a pattern: `pattern.withContext(...)`.

Returns a new pattern with the given function applied to the context field of every hap.

**Parameters**

- `func` (`Function`)

### withHap

`withHap(func: Function) -> Pattern`

Pattern method — call it on a pattern: `pattern.withHap(...)`.

As with `withHaps`, but applies the function to every hap, rather than every list of haps.

**Parameters**

- `func` (`Function`)

### withHaps

`withHaps(func: Function) -> Pattern`

Pattern method — call it on a pattern: `pattern.withHaps(...)`.

Returns a new pattern with the given function applied to the list of haps returned by every query.

**Parameters**

- `func` (`Function`)

### withHapSpan

`withHapSpan(func: Function) -> Pattern`

Pattern method — call it on a pattern: `pattern.withHapSpan(...)`.

Similar to `withQuerySpan`, but the function is applied to the timespans
of all haps returned by pattern queries (both `part` timespans, and where
present, `whole` timespans).

**Parameters**

- `func` (`Function`)

### withHapTime

`withHapTime(func: Function) -> Pattern`

Pattern method — call it on a pattern: `pattern.withHapTime(...)`.

As with `withHapSpan`, but the function is applied to both the
begin and end time of the hap timespans.

**Parameters**

- `func` (`Function`): the function to apply

### within

`within(start: Number, end: Number, func: Function)`

Use within to apply a function to only a part of a pattern.

**Parameters**

- `start` (`Number`): start within cycle (0 - 1)
- `end` (`Number`): end within cycle (0 - 1). Must be > start
- `func` (`Function`): function to be applied to the sub-pattern

### withLoc

`withLoc(start: Number, end: Number) -> Pattern`

Pattern method — call it on a pattern: `pattern.withLoc(...)`.

Returns a new pattern with the given location information added to the
context of every hap.

**Parameters**

- `start` (`Number`): start offset
- `end` (`Number`): end offset

### withQuerySpan

`withQuerySpan(func: Function) -> Pattern`

Pattern method — call it on a pattern: `pattern.withQuerySpan(...)`.

Returns a new pattern, where the given function is applied to the query
timespan before passing it to the original pattern.

**Parameters**

- `func` (`Function`): the function to apply

### withQueryTime

`withQueryTime(func: Function) -> Pattern`

Pattern method — call it on a pattern: `pattern.withQueryTime(...)`.

As with `withQuerySpan`, but the function is applied to both the
begin and end time of the query timespan.

**Parameters**

- `func` (`Function`): the function to apply

### withSeed

`withSeed(func: Function, pat: Pattern) -> Pattern`

Modify a pattern by applying a function to the `randomSeed` control if present

**Parameters**

- `func` (`Function`): Function from seed (or undefined) to seed (or undefined)
- `pat` (`Pattern`): Pattern to update

### withValue

`withValue(func: Function) -> Pattern`

Pattern method — call it on a pattern: `pattern.withValue(...)`.

Synonyms: fmap

Returns a new pattern, with the function applied to the value of
each hap. It has the alias `fmap`.

**Parameters**

- `func` (`Function`): to to apply to the value

**Examples**

```js
"0 1 2".withValue(v => v + 10).log()
```

### worklet

`worklet(src: String, inputs: Number|Pattern) -> Pattern`

Pattern method — call it on a pattern: `pattern.worklet(...)`.

Creates a worklet effect. Typically derived by writing K(...) in the REPL which will parse
Kabelsalat code.

**Parameters**

- `src` (`String`): Source code of the worklet update function
- `inputs` (`Number|Pattern`): Worklet inputs

### wt

`wt(position: Number|Pattern)`

Synonyms: wavetablePosition

Position in the wavetable of the wavetable oscillator

**Parameters**

- `position` (`Number|Pattern`): Position in the wavetable from 0 to 1

**Examples**

```js
s("squelch").bank("wt_digital").seg(8).note("F1").wt("0 0.25 0.5 0.75 1")
```

### wtattack

`wtattack(time: Number|Pattern)`

Synonyms: wtatt

Attack time of the wavetable oscillator's position envelope

**Parameters**

- `time` (`Number|Pattern`): attack time in seconds

### wtdc

`wtdc(dcoffset: Number|Pattern)`

DC offset of the LFO for the wavetable oscillator's position

**Parameters**

- `dcoffset` (`Number|Pattern`): dc offset. set to 0 for unipolar

### wtdecay

`wtdecay(time: Number|Pattern)`

Synonyms: wtdec

Decay time of the wavetable oscillator's position envelope

**Parameters**

- `time` (`Number|Pattern`): decay time in seconds

### wtdepth

`wtdepth(depth: Number|Pattern)`

Depth of the LFO for the wavetable oscillator's position

**Parameters**

- `depth` (`Number|Pattern`): depth of modulation

### wtenv

`wtenv(amount: Number|Pattern)`

Amount of envelope applied wavetable oscillator's position envelope

**Parameters**

- `amount` (`Number|Pattern`): between 0 and 1

### wtphaserand

`wtphaserand(amount: Number|Pattern)`

Synonyms: wavetablePhaseRand

Amount of randomness of the initial phase of the wavetable oscillator.

**Parameters**

- `amount` (`Number|Pattern`): Randomness of the initial phase. Between 0 (not random) and 1 (fully random)

**Examples**

```js
s("basique").bank("wt_digital").seg(16).wtphaserand("<0 1>")
```

### wtrate

`wtrate(rate: Number|Pattern)`

Rate of the LFO for the wavetable oscillator's position

**Parameters**

- `rate` (`Number|Pattern`): rate in hertz

### wtrelease

`wtrelease(time: Number|Pattern)`

Synonyms: wtrel

Release time of the wavetable oscillator's position envelope

**Parameters**

- `time` (`Number|Pattern`): release time in seconds

### wtshape

`wtshape(shape: Number|Pattern)`

Shape of the LFO for the wavetable oscillator's position

**Parameters**

- `shape` (`Number|Pattern`): Shape of the lfo (0, 1, 2,..)

### wtskew

`wtskew(skew: Number|Pattern)`

Skew of the LFO for the wavetable oscillator's position

**Parameters**

- `skew` (`Number|Pattern`): How much to bend the LFO shape

### wtsustain

`wtsustain(gain: Number|Pattern)`

Synonyms: wtsus

Sustain time of the wavetable oscillator's position envelope

**Parameters**

- `gain` (`Number|Pattern`): sustain level (0 to 1)

### wtsync

`wtsync(rate: Number|Pattern)`

cycle synced rate of the LFO for the wavetable oscillator's position

**Parameters**

- `rate` (`Number|Pattern`): rate in cycles

### xfade

`xfade(...)`

Requires arguments: at least 3.

Cross-fades between left and right from 0 to 1:

- 0 = (full left, no right)
- .5 = (both equal)
- 1 = (no left, full right)

**Examples**

```js
xfade(s("bd*2"), "<0 .25 .5 .75 1>", s("hh*8"))
```

### zip

`zip() -> Pattern`

*Experimental*

'zips' together the steps of the provided patterns. This can create a long repetition, taking place over a single, dense cycle.
Using `pace` to set the number of steps per cycle is therefore usually recommended.

**Examples**

```js
zip("e f", "e f g", "g [f e] a f4 c").note()
   .sound("folkharp")
   .pace(8)
```

### zoom

`zoom()`

Plays a portion of a pattern, specified by the beginning and end of a time span. The new resulting pattern is played over the time period of the original pattern:

**Examples**

```js
s("bd*2 hh*3 [sd bd]*2 perc").zoom(0.25, 0.75)
// s("hh*3 [sd bd]*2") // equivalent
```

## @strudel/tonal

### addVoicings

`addVoicings(name: String, dictionary: Object, range: Array) -> Pattern`

Adds a new custom voicing dictionary.

**Parameters**

- `name` (`String`): identifier for the voicing dictionary
- `dictionary` (`Object`): maps chord symbol to possible voicings
- `range` (`Array`): min, max note

**Examples**

```js
addVoicings('cookie', {
  7: ['3M 7m 9M 12P 15P', '7m 10M 13M 16M 19P'],
  '^7': ['3M 6M 9M 12P 14M', '7M 10M 13M 16M 19P'],
  m7: ['8P 11P 14m 17m 19P', '5P 8P 11P 14m 17m'],
  m7b5: ['3m 5d 8P 11P 14m', '5d 8P 11P 14m 17m'],
  o7: ['3m 6M 9M 11A 15P'],
  '7alt': ['3M 7m 10m 13m 15P'],
  '7#11': ['7m 10m 13m 15P 17m'],
}, ['C3', 'C6'])
"<C^7 A7 Dm7 G7>".voicings('cookie').note()
```

### rootNotes

`rootNotes(octave: octave) -> Pattern`

Pattern method — call it on a pattern: `pattern.rootNotes(...)`.

Maps the chords of the incoming pattern to root notes in the given octave.

**Parameters**

- `octave` (`octave`): octave to use

**Examples**

```js
"<C^7 A7 Dm7 G7>".rootNotes(2).note()
```

### scale

`scale(scale: String) -> Pattern`

Turns numbers into notes in the scale (zero indexed) or quantizes notes to a scale.

When describing notes via numbers, note that negative numbers can be used to wrap backwards
in the scale as well as sharps or flats to produce notes outside of the scale.

Also sets scale for other scale operations, like `Pattern#scaleTranspose`.

A scale consists of a root note (e.g. `c4`, `c`, `f#`, `bb4`) followed by semicolon (':') and then a scale type (https://github.com/tonaljs/tonal/blob/main/packages/scale-type/data.ts).

The scale name must be written without spaces (because it would be interpreted as a multi-step pattern otherwise).
If your scale name includes spaces, replace them with colons.

The root note defaults to octave 3, if no octave number is given.

**Parameters**

- `scale` (`String`): Name of scale

**Examples**

```js
n("0 2 4 6 4 2").scale("C:major")
```

```js
n("[0,7] 4 [2,7] 4")
.scale("C:<major minor>/2")
.s("piano")
```

```js
n(rand.range(0,12).segment(8))
.scale("C:ritusen")
.s("piano")
```

```js
n("<[0,7b] [-4# -4] [-2,7##] 4 [0,7] [-4# -4b] [-2,7###] 4b>*4")
.scale("C:<major minor>/2")
.s("piano")
```

```js
note("C1*16").transpose(irand(36)).scale('Cb2 major').scaleTranspose(3)
```

```js
n("[0 0] [1 2] [3 4] [5 6]").scale("C:major:blues")
```

### scaleTranspose

`scaleTranspose(offset: offset) -> Pattern`

Pattern method — call it on a pattern: `pattern.scaleTranspose(...)`.

Synonyms: scaleTrans, strans

Transposes notes inside the scale by the number of steps.
Expected to be called on a Pattern which already has a `Pattern#scale`

**Parameters**

- `offset` (`offset`): number of steps inside the scale

**Examples**

```js
"-8 [2,4,6]"
.scale('C4 bebop major')
.scaleTranspose("<0 -1 -2 -3 -4 -5 -6 -4>")
.note()
```

### transpose

`transpose(amount: String|Number) -> Pattern`

Pattern method — call it on a pattern: `pattern.transpose(...)`.

Synonyms: trans

Change the pitch of each value by the given amount. Expects numbers or note strings as values.
The amount can be given as a number of semitones or as a string in interval short notation.
If you don't care about enharmonic correctness, just use numbers. Otherwise, pass the interval of
the form: ST where S is the degree number and T the type of interval with

- M = major
- m = minor
- P = perfect
- A = augmented
- d = diminished

Examples intervals:

- 1P = unison
- 3M = major third
- 3m = minor third
- 4P = perfect fourth
- 4A = augmented fourth
- 5P = perfect fifth
- 5d = diminished fifth

**Parameters**

- `amount` (`String|Number`): Either number of semitones or interval string.

**Examples**

```js
"c2 c3".fast(2).transpose("<0 -2 5 3>".slow(2)).note()
```

```js
"c2 c3".fast(2).transpose("<1P -2M 4P 3m>".slow(2)).note()
```

### voicing

`voicing() -> Pattern`

Turns chord symbols into voicings. You can use the following control params:

- `chord`: Note, followed by chord symbol, e.g. C Am G7 Bb^7
- `dict`: voicing dictionary to use, falls back to default dictionary
- `anchor`: the note that is used to align the chord
- `mode`: how the voicing is aligned to the anchor
  - `below`: top note <= anchor
  - `duck`: top note <= anchor, anchor excluded
  - `above`: bottom note >= anchor
  - `root`: bottom note is the lowest root of the chord >= anchor
  - `oldabove` : old (buggy) behavior of above, kept for legacy reason
  - `oldroot` : old (buggy) behavior of root, kept for legacy reason
- `offset`: whole number that shifts the voicing up or down to the next voicing
- `n`: if set, the voicing is played like a scale. Overshooting numbers will be octaved

All of the above controls are optional, except `chord`.
If you pass a pattern of strings to voicing, they will be interpreted as chords.

**Examples**

```js
n("0 1 2 3").chord("<C Am F G>").voicing()
```

### voicings

`voicings(dictionary: String) -> Pattern`

Pattern method — call it on a pattern: `pattern.voicings(...)`.

DEPRECATED: still works, but it is recommended you use .voicing instead (without s).
Turns chord symbols into voicings, using the smoothest voice leading possible.
Uses chord-voicings package (https://github.com/felixroos/chord-voicings#chord-voicings).

**Parameters**

- `dictionary` (`String`): which voicing dictionary to use.

**Examples**

```js
stack("<C^7 A7 Dm7 G7>".voicings('lefthand'), "<C3 A2 D3 G2>").note()
```

## @strudel/xen

### ftranspose

`ftranspose(amt: Number, edoSize: Number) -> Pattern`

Synonyms: ftrans, fTrans, ftranspose, fTranspose

Frequency transpose. Assumes pattern either has `freq` set, or has values that can be interpreted as frequencies
amt has optional `edoSize` param, defaults to 12.
If haps have edoSize param set, such as from the output of `xen("31edo")`,
`ftrans` will fallback to that instead of 12 as the default.

Transposes the frequency by `amt` edoSteps

**Parameters**

- `amt` (`Number`)
- `edoSize` (`Number`): (optional)

**Examples**

```js
i("0 1 2").xen("12edo").ftrans("7")
// n("0 1 2").scale("A:chromatic").trans("7")
```

```js
i("0 8 18").xen("31edo").ftrans("<8 -8>")
```

```js
// to transpose by steps of an edo, use "step:edo" :
i("0 7 8 18").xen("31edo").ftrans("<0 1:31 1:12>")
```

```js
// it can also work with frequency values directly
freq("200 300 400").ftrans("<0 7:31 7>")
```

### tune

`tune(scale: String|Number[]) -> Pattern`

Pattern method — call it on a pattern: `pattern.tune(...)`.

Assumes pattern contains numerical scale degrees on the `i` control (see examples below). Accepts a scale name or list of frequencies (see all available names at the link on the reference). Returns a new pattern with all values mapped to a frequency ratio. Similar to `xen`.

**Parameters**

- `scale` (`String|Number[]`)

**Examples**

```js
i("0 1 2 3 4 5").tune("hexany15").mul("220").freq()
```

```js
// You can set your root to be a
// particular note with getFreq:
i("4 8 9 10 - - 5 7 9 11 - -").tune("tranh3")
  .mul(getFreq('c3'))
  .freq().clip(.5).room(1)
```

```js
// You can also give tune a list of
// frequencies to use as the scale:
i("0 1 2 3 4").tune([
  261.6255653006,
  302.72962012827,
  350.29154279212,
  405.32593044476,
  469.00678383895,
  523.2511306012
]).mul(220).freq();
```

### withBase

`withBase(base: Number, originalBase?: Number) -> Pattern`

Assumes pattern of frequencies tuned to some `base` frequency, such as the output of `xen`
Because `xen` defaults to `220Hz`, so will `withBase`.
but you can specify a different original base with the standard optional array syntax '`:`'

**Parameters**

- `base` (`Number`)
- `originalBase?` (`Number`): originalBase

**Examples**

```js
i("[0 1 2 3] [3 4] [4 3 2 1]").xen("hexany23").withBase("<220 [300 200]>")
```

```js
mini([1 / 1, 16 / 15, 9 / 8, 6 / 5, 5 / 4].join(' ')).withBase("220:1")
// mini([1 / 1, 16 / 15, 9 / 8, 6 / 5, 5 / 4].join(' ')).mul(220).freq()
```

### xen

`xen(scaleNameOrRatios: String|Number[]) -> Pattern`

Pattern method — call it on a pattern: `pattern.xen(...)`.

Assumes a numerical pattern of scale steps, and a scale. Scales accepted are all preset scale names of `tune`, arbitrary edos such as 31edo, or an array of frequency ratios. Assumes scales repeat at octave (2/1). Returns a new pattern with all values mapped to their associated frequency, assuming a base frequency of 220hz.

**Parameters**

- `scaleNameOrRatios` (`String|Number[]`)

**Examples**

```js
// A minor triad in 31edo:
i("0 8 18").xen("31edo").piano()
```

```js
// You can also use xen with frequency ratios.
// This is equivalent to the above:
i("0 1 2").xen([
  Math.pow(2, 0/31),
  Math.pow(2, 8/31),
  Math.pow(2, 18/31),
]).piano()
```

```js
// xen also supports all scale names that
// tune does:
i("0 1 2 3 4 5").xen("hexany15")
// equiv to:
// "0 1 2 3 4 5".tune("hexany15").mul("220").freq()
```

```js
i("0 1 2 3 4 5 6 7").xen("<5edo 10edo 15edo hexany15>")
```

## @strudel/edo

### edoScale

`edoScale(scale: String) -> Pattern`

Turns numbers into notes in the given EDO scale (zero indexed).

An EDO scale definition looks like this:

e.g. C:LLsLLLs:2:1 <- this is the C major scale, 12 EDO

e.g. C:LLsLLL:3:1 <- this is the Gorgo 6 note scale, 16 EDO

An EDO scale, e.g. C:LLsLLLs:2:1, consists of a root note (e.g. C)
followed by semicolon (':')
and then a Large/small step notation sequence (https://en.xen.wiki/w/MOS_scale)
(e.g. LLsLLLs)
followed by semicolon, then the large step size (e.g. 2)
followed by semicolon, then the small step size (e.g. 1).

The number of divisions of the octave is calculated as the sum
of the steps in the EDO scale definition.

e.g. C:LLsLLLs:2:1 is 2+2+1+2+2+2+1 = 12 EDO, 7 note scale

e.g. C:LLsLLL:3:1 is 3+3+1+3+3+3 = 16 EDO, 6 note scale

The root note defaults to octave 3, if no octave number is given.

**Parameters**

- `scale` (`String`): Definition of EDO scale.

**Examples**

```js
n("0 2 4 6 4 2").edoScale("C:LLsLLLs:2:1")
```

```js
n("[0,7] 4 [2,7] 4")
.edoScale("G2:<LLsLLL LLLLsL>:3:1")
.s("piano")._pitchwheel()
```

```js
n(rand.range(0,5).segment(6))
.edoScale("<G2 C3>:LLsLL:3:1")
.s("piano")._pitchwheel()
```

## @strudel/draw

### pianoroll

`pianoroll(options: Object, cycles: Integer, playhead: Number, vertical: Boolean, labels: Boolean, flipTime: Boolean, flipValues: Boolean, overscan: Number, hideNegative: Boolean, smear: Boolean, fold: Boolean, active: String, inactive: String, background: String, playheadColor: String, fill: Boolean, fillActive: Boolean, stroke: Boolean, strokeActive: Boolean, hideInactive: Boolean, colorizeInactive: Boolean, fontFamily: String, minMidi: Integer, maxMidi: Integer, autorange: Boolean)`

Synonyms: punchcard

Visualises a pattern as a scrolling 'pianoroll', displayed in the background of the editor. To show a pianoroll for all running patterns, use `all(pianoroll)`. To have a pianoroll appear below
a pattern instead, prefix with `_`, e.g.: `sound("bd sd")._pianoroll()`.

**Parameters**

- `options` (`Object`): Object containing all the optional following parameters as key value pairs:
- `cycles` (`Integer`): number of cycles to be displayed at the same time - defaults to 4
- `playhead` (`Number`): location of the active notes on the time axis - 0 to 1, defaults to 0.5
- `vertical` (`Boolean`): displays the roll vertically - 0 by default
- `labels` (`Boolean`): displays labels on individual notes (see the label function) - 0 by default
- `flipTime` (`Boolean`): reverse the direction of the roll - 0 by default
- `flipValues` (`Boolean`): reverse the relative location of notes on the value axis - 0 by default
- `overscan` (`Number`): lookup X cycles outside of the cycles window to display notes in advance - 1 by default
- `hideNegative` (`Boolean`): hide notes with negative time (before starting playing the pattern) - 0 by default
- `smear` (`Boolean`): notes leave a solid trace - 0 by default
- `fold` (`Boolean`): notes takes the full value axis width - 0 by default
- `active` (`String`): hexadecimal or CSS color of the active notes - defaults to #FFCA28
- `inactive` (`String`): hexadecimal or CSS color of the inactive notes - defaults to #7491D2
- `background` (`String`): hexadecimal or CSS color of the background - defaults to transparent
- `playheadColor` (`String`): hexadecimal or CSS color of the line representing the play head - defaults to white
- `fill` (`Boolean`): notes are filled with color (otherwise only the label is displayed) - 0 by default
- `fillActive` (`Boolean`): active notes are filled with color - 0 by default
- `stroke` (`Boolean`): notes are shown with colored borders - 0 by default
- `strokeActive` (`Boolean`): active notes are shown with colored borders - 0 by default
- `hideInactive` (`Boolean`): only active notes are shown - 0 by default
- `colorizeInactive` (`Boolean`): use note color for inactive notes - 1 by default
- `fontFamily` (`String`): define the font used by notes labels - defaults to 'monospace'
- `minMidi` (`Integer`): minimum note value to display on the value axis - defaults to 10
- `maxMidi` (`Integer`): maximum note value to display on the value axis - defaults to 90
- `autorange` (`Boolean`): automatically calculate the minMidi and maxMidi parameters - 0 by default

**Examples**

```js
note("c2 a2 eb2")
.euclid(5,8)
.s('sawtooth')
.lpenv(4).lpf(300)
.pianoroll({ labels: 1 })
```

### pitchwheel

`pitchwheel(hapcircles: Number, circle: Number, edo: Number, root: String, thickness: Number, hapRadius: Number, mode: String, margin: Number)`

Renders a pitch circle to visualize frequencies within one octave

**Parameters**

- `hapcircles` (`Number`)
- `circle` (`Number`)
- `edo` (`Number`)
- `root` (`String`)
- `thickness` (`Number`)
- `hapRadius` (`Number`)
- `mode` (`String`)
- `margin` (`Number`)

**Examples**

```js
n("0 .. 12").scale("C:chromatic")
.s("sawtooth")
.lpf(500)
._pitchwheel()
```

### spiral

`spiral(options: Object, stretch: Number, size: Number, thickness: Number, cap: String, inset: String, playheadColor: String, playheadLength: Number, playheadThickness: Number, padding: Number, steady: Number, activeColor: Number, inactiveColor: Number, colorizeInactive: Boolean, fade: Boolean, logSpiral: Boolean)`

Displays a spiral visual.

**Parameters**

- `options` (`Object`): Object containing all the optional following parameters as key value pairs:
- `stretch` (`Number`): controls the rotations per cycle ratio, where 1 = 1 cycle / 360 degrees
- `size` (`Number`): the diameter of the spiral
- `thickness` (`Number`): line thickness
- `cap` (`String`): style of line ends: butt (default), round, square
- `inset` (`String`): number of rotations before spiral starts (default 3)
- `playheadColor` (`String`): color of playhead, defaults to white
- `playheadLength` (`Number`): length of playhead in rotations, defaults to 0.02
- `playheadThickness` (`Number`): thickness of playheadrotations, defaults to thickness
- `padding` (`Number`): space around spiral
- `steady` (`Number`): steadyness of spiral vs playhead. 1 = spiral doesn't move, playhead does.
- `activeColor` (`Number`): color of active segment. defaults to foreground of theme
- `inactiveColor` (`Number`): color of inactive segments. defaults to gutterForeground of theme
- `colorizeInactive` (`Boolean`): wether or not to colorize inactive segments, defaults to 0
- `fade` (`Boolean`): wether or not past and future should fade out. defaults to 1
- `logSpiral` (`Boolean`): wether or not the spiral should be logarithmic. defaults to 0

**Examples**

```js
note("c2 a2 eb2")
.euclid(5,8)
.s('sawtooth')
.lpenv(4).lpf(300)
._spiral({ steady: .96 })
```

### wordfall

`wordfall()`

Displays a vertical pianoroll with event labels.
Supports all the same options as pianoroll.

## @strudel/webaudio

### fscope

`fscope(color: String, scale: Number, pos: Number, lean: Number, min: Number, max: Number)`

Renders an oscilloscope for the frequency domain of the audio signal.

**Parameters**

- `color` (`String`): line color as hex or color name. defaults to white.
- `scale` (`Number`): scales the y-axis. Defaults to 0.25
- `pos` (`Number`): y-position relative to screen height. 0 = top, 1 = bottom of screen
- `lean` (`Number`): y-axis alignment where 0 = top and 1 = bottom
- `min` (`Number`): min value
- `max` (`Number`): max value

**Examples**

```js
s("sawtooth").fscope()
```

### scope

`scope(config: Object, align: Boolean, color: String, thickness: Number, scale: Number, pos: Number, trigger: Number)`

Synonyms: tscope

Renders an oscilloscope for the time domain of the audio signal.

**Parameters**

- `config` (`Object`): optional config with options:
- `align` (`Boolean`): if 1, the scope will be aligned to the first zero crossing. defaults to 1
- `color` (`String`): line color as hex or color name. defaults to white.
- `thickness` (`Number`): line thickness. defaults to 3
- `scale` (`Number`): scales the y-axis. Defaults to 0.25
- `pos` (`Number`): y-position relative to screen height. 0 = top, 1 = bottom of screen
- `trigger` (`Number`): amplitude value that is used to align the scope. defaults to 0.

**Examples**

```js
s("sawtooth")._scope()
```

### spectrum

`spectrum(config: Object, thickness: Integer, speed: Integer, min: Integer, max: Integer)`

Renders a spectrum analyzer for the incoming audio signal.

**Parameters**

- `config` (`Object`): optional config with options:
- `thickness` (`Integer`): line thickness in px (default 3)
- `speed` (`Integer`): scroll speed (default 1)
- `min` (`Integer`): min db (default -80)
- `max` (`Integer`): max db (default 0)

**Examples**

```js
n("<0 4 <2 3> 1>*3")
.off(1/8, add(n(5)))
.off(1/5, add(n(7)))
.scale("d3:minor:pentatonic")
.s('sine')
.dec(.3).room(.5)
._spectrum()
```

## @strudel/superdough

### aliasBank

`aliasBank(bank: String, alias: String)`

Register an alias for a bank of sounds.
Optionally accepts a single argument map of bank aliases.
Optionally accepts a single argument string of a path to a JSON file containing bank aliases.

**Parameters**

- `bank` (`String`): The bank to alias
- `alias` (`String`): The alias to use for the bank

### getDuration

`getDuration(sampleName: String, n?: Number)`

Returns the duration, in seconds, of the given sample.
Has optional param `n` (for instance, the `2` in `s("casio:2")`)

Note: `must` be called with await, otherwise you'll get a pending Promise object.

**Parameters**

- `sampleName` (`String`)
- `n?` (`Number`): n

**Examples**

```js
// Set a patterns cycle length to exactly the length of the sample
samples('github:tidalcycles/dirt-samples')
let k = await getDuration('sax')
s("sax").cps(1/k)
```

### samples

`samples()`

Loads a collection of samples to use with `s`

**Examples**

```js
samples('github:tidalcycles/dirt-samples');
s("[bd ~]*2, [~ hh]*2, ~ sd")
```

```js
samples({
 bd: '808bd/BD0000.WAV',
 sd: '808sd/SD0010.WAV'
 }, 'https://raw.githubusercontent.com/tidalcycles/Dirt-Samples/master/');
s("[bd ~]*2, [~ hh]*2, ~ sd")
```

### setGainCurve

`setGainCurve(function: Function)`

Apply a function to all gains provided in patterns. Can be used to rescale gain to be
quadratic, exponential, etc. rather than linear

**Parameters**

- `function` (`Function`): to apply to all gain values

**Examples**

```js
setGainCurve((x) => x * x) // quadratic gain
s("bd*4").gain(0.5) // equivalent to 0.25 gain normally
```

### setMaxPolyphony

`setMaxPolyphony(Max: Number)`

Set the max polyphony. If notes are ringing out via `release` then they will
start to die out in first-in-first-out order once the max polyphony has been hit

**Parameters**

- `Max` (`Number`): polyphony. Defaults to 128

**Examples**

```js
setMaxPolyphony(4)
n(irand(24).seg(8)).scale("C#3:minor").room(1).release(4).gain(0.5)
```

### soundAlias

`soundAlias(original: String, alias: String)`

Register an alias for a sound.

**Parameters**

- `original` (`String`): The original sound name
- `alias` (`String`): The alias to use for the sound

### tables

`tables()`

Loads a collection of wavetables to use with `s`

## @strudel/dough

### dough

`dough() -> Pattern`

Pattern method — call it on a pattern: `pattern.dough(...)`.

Uses dough as the audio engine. more info at https://dough.strudel.cc

**Examples**

```js
$: chord("<Dm9 Dm11 Dm7>").offset("<-1 0 1 0>").voicing()
.phaser(3).fm(1.4).fmh(1.01)
.dough()
```

### initDough

`initDough()`

initializes dough ahead of time. you don't need to use this, but if you do, you can await it, so dough is ready before the pattern starts.

**Examples**

```js
await initDough()
$: chord("<Dm9 Dm11 Dm7>").offset("<-1 0 1 0>").voicing()
.phaser(3).fm(1.4).fmh(1.01)
.dough()
```

## @strudel/midi

### defaultmidimap

`defaultmidimap()`

configures the default midimap, which is used when no "midimap" port is set

**Examples**

```js
defaultmidimap({ lpf: 74 })
$: note("c a f e").midi();
$: lpf(sine.slow(4).segment(16)).midi();
```

### midi

`midi(midiport: String|Number, options: Object)`

Pattern method — call it on a pattern: `pattern.midi(...)`.

MIDI output: Opens a MIDI output port.

**Parameters**

- `midiport` (`String|Number`): MIDI device name or index defaulting to 0
- `options` (`Object`): Additional MIDI configuration options

**Examples**

```js
note("c4").midichan(1).midi('IAC Driver Bus 1')
```

### midikeys

`midikeys(input: String|Number) -> Function`

MIDI keyboard: Opens a MIDI input port to receive MIDI keyboard messages.

The note length is fixed as Superdough is not currently set up for undetermined
note durations

The 'midichan' control value contains the number of the channel the note is coming from
so it could be filtered or manipulated further in the chain.

**Parameters**

- `input` (`String|Number`): MIDI device name or index defaulting to 0

**Returns** `Function` — A function that produces a pattern. When queried, the pattern will produces the most recently played midi notes and velocities, lasting for the specified duration

**Examples**

```js
const kb = await midikeys('Arturia KeyStep 32')
kb().s("tri").lpf(80).lpe(6).lpd(0.1).room(2).delay(0.35)
```

```js
const kb = await midikeys('Arturia KeyStep 32')
kb("0.5 1")
  .s("saw")
  .add(note(rand.mul(0.3)))
  .lpf(1000).lpe(2).room(0.5)
```

```js
// discard all notes not coming out from midi channel 2
const kb = await midikeys('Arturia KeyStep 32')
kb().filterValues(v=>v.midichan==2).s("tri")
```

### midimaps

`midimaps()`

Adds midimaps to the registry. Inside each midimap, control names (e.g. lpf) are mapped to cc numbers.

**Examples**

```js
midimaps({ mymap: { lpf: 74 } })
$: note("c a f e")
.lpf(sine.slow(4))
.midimap('mymap')
.midi()
```

```js
midimaps({ mymap: {
  lpf: { ccn: 74, min: 0, max: 20000, exp: 0.5 }
}})
$: note("c a f e")
.lpf(sine.slow(2).range(400,2000))
.midimap('mymap')
.midi()
```

### midin

`midin(input: String|Number) -> Function`

MIDI input: Opens a MIDI input port to receive MIDI control change messages.

The output is a function that accepts a midi cc value to query as well as (optionally) a midi channel

**Parameters**

- `input` (`String|Number`): MIDI device name or index defaulting to 0

**Returns** `Function` — A function from (cc, channel?) to a pattern. When queried, the pattern will produces the most recently received midi value (normalized to 0 to 1) that came through that cc number (and channel, if provided)

**Examples**

```js
const cc = await midin('IAC Driver Bus 1')
note("c a f e").lpf(cc(0).range(0, 1000)).lpq(cc(1).range(0, 10)).sound("sawtooth")
```

```js
const allCC = await midin('IAC Driver Bus 1')
const cc = (ccNum) => allCC(ccNum, 2) // just channel 2
note("c a f e").s("saw")
  .when(cc(0).gt(0), x => x.postgain(0))
```

## @strudel/osc

### osc

`osc() -> Pattern`

Pattern method — call it on a pattern: `pattern.osc(...)`.

Sends each hap as an OSC message, which can be picked up by SuperCollider or any other OSC-enabled software.
For more info, read MIDI & OSC in the docs (https://strudel.cc/learn/input-output/)

## @strudel/motion

### absoluteOrientationAlpha

`absoluteOrientationAlpha` — a Pattern value, not a function. Use it directly, e.g. `absoluteOrientationAlpha.range(...)`.

Synonyms: absOriA, absOriZ, absoluteOrientationZ

The device's absolute orientation alpha value ranges from 0 to 1.

**Examples**

```js
n(absoluteOrientationAlpha.segment(4).range(0,7)).scale("C:minor")
```

### absoluteOrientationBeta

`absoluteOrientationBeta` — a Pattern value, not a function. Use it directly, e.g. `absoluteOrientationBeta.range(...)`.

Synonyms: absOriB, absOriX, absoluteOrientationX

The device's absolute orientation beta value ranges from 0 to 1.

**Examples**

```js
n(absoluteOrientationBeta.segment(4).range(0,7)).scale("C:minor")
```

### absoluteOrientationGamma

`absoluteOrientationGamma` — a Pattern value, not a function. Use it directly, e.g. `absoluteOrientationGamma.range(...)`.

Synonyms: absOriG, absOriY, absoluteOrientationY

The device's absolute orientation gamma value ranges from 0 to 1.

**Examples**

```js
n(absoluteOrientationGamma.segment(4).range(0,7)).scale("C:minor")
```

### accelerationX

`accelerationX` — a Pattern value, not a function. Use it directly, e.g. `accelerationX.range(...)`.

Synonyms: accX

The accelerometer's x-axis value ranges from 0 to 1.

**Examples**

```js
n(accelerationX.segment(4).range(0,7)).scale("C:minor")
```

### accelerationY

`accelerationY` — a Pattern value, not a function. Use it directly, e.g. `accelerationY.range(...)`.

Synonyms: accY

The accelerometer's y-axis value ranges from 0 to 1.

**Examples**

```js
n(accelerationY.segment(4).range(0,7)).scale("C:minor")
```

### accelerationZ

`accelerationZ` — a Pattern value, not a function. Use it directly, e.g. `accelerationZ.range(...)`.

Synonyms: accZ

The accelerometer's z-axis value ranges from 0 to 1.

**Examples**

```js
n(accelerationZ.segment(4).range(0,7)).scale("C:minor")
```

### gravityX

`gravityX` — a Pattern value, not a function. Use it directly, e.g. `gravityX.range(...)`.

Synonyms: gravX

The device's gravity x-axis value ranges from 0 to 1.

**Examples**

```js
n(gravityX.segment(4).range(0,7)).scale("C:minor")
```

### gravityY

`gravityY` — a Pattern value, not a function. Use it directly, e.g. `gravityY.range(...)`.

Synonyms: gravY

The device's gravity y-axis value ranges from 0 to 1.

**Examples**

```js
n(gravityY.segment(4).range(0,7)).scale("C:minor")
```

### gravityZ

`gravityZ` — a Pattern value, not a function. Use it directly, e.g. `gravityZ.range(...)`.

Synonyms: gravZ

The device's gravity z-axis value ranges from 0 to 1.

**Examples**

```js
n(gravityZ.segment(4).range(0,7)).scale("C:minor")
```

### orientationAlpha

`orientationAlpha` — a Pattern value, not a function. Use it directly, e.g. `orientationAlpha.range(...)`.

Synonyms: oriA, oriZ, orientationZ

The device's orientation alpha value ranges from 0 to 1.

**Examples**

```js
n(orientationAlpha.segment(4).range(0,7)).scale("C:minor")
```

### orientationBeta

`orientationBeta` — a Pattern value, not a function. Use it directly, e.g. `orientationBeta.range(...)`.

Synonyms: oriB, oriX, orientationX

The device's orientation beta value ranges from 0 to 1.

**Examples**

```js
n(orientationBeta.segment(4).range(0,7)).scale("C:minor")
```

### orientationGamma

`orientationGamma` — a Pattern value, not a function. Use it directly, e.g. `orientationGamma.range(...)`.

Synonyms: oriG, oriY, orientationY

The device's orientation gamma value ranges from 0 to 1.

**Examples**

```js
n(orientationGamma.segment(4).range(0,7)).scale("C:minor")
```

### rotationAlpha

`rotationAlpha` — a Pattern value, not a function. Use it directly, e.g. `rotationAlpha.range(...)`.

Synonyms: rotA, rotZ, rotationZ

The device's rotation around the alpha-axis value ranges from 0 to 1.

**Examples**

```js
n(rotationAlpha.segment(4).range(0,7)).scale("C:minor")
```

### rotationBeta

`rotationBeta` — a Pattern value, not a function. Use it directly, e.g. `rotationBeta.range(...)`.

Synonyms: rotB, rotX, rotationX

The device's rotation around the beta-axis value ranges from 0 to 1.

**Examples**

```js
n(rotationBeta.segment(4).range(0,7)).scale("C:minor")
```

### rotationGamma

`rotationGamma` — a Pattern value, not a function. Use it directly, e.g. `rotationGamma.range(...)`.

Synonyms: rotG, rotY, rotationY

The device's rotation around the gamma-axis value ranges from 0 to 1.

**Examples**

```js
n(rotationGamma.segment(4).range(0,7)).scale("C:minor")
```

## @strudel/csound

### csoundm

`csoundm()`

Sends notes to Csound for rendering with MIDI semantics. The hap value is
translated to these Csound pfields:

p1 -- Csound instrument either as a number (1-based, can be a fraction),
or as a string name.
p2 -- time in beats (usually seconds) from start of performance.
p3 -- duration in beats (usually seconds).
p4 -- MIDI key number (as a real number, not an integer but in [0, 127].
p5 -- MIDI velocity (as a real number, not an integer but in [0, 127].
p6 -- Strudel controls, as a string.

## @strudel/codemirror

### markcss

`markcss()`

Overrides the css of highlighted events. Make sure to use single quotes!

**Examples**

```js
note("c a f e")
.markcss('text-decoration:underline')
```

### slider

`slider(value: Number, min: Number, max: Number, step: Number)`

Displays a slider widget to allow the user manipulate a value

**Parameters**

- `value` (`Number`): Initial value
- `min` (`Number`): Minimum value - optional, defaults to 0
- `max` (`Number`): Maximum value - optional, defaults to 1
- `step` (`Number`): Step size - optional

## Aliases

Aliases are alternative names accepted in the REPL. They have no entry of their own; the target below
documents the behaviour. An alias that is also a real symbol is documented under its own name instead.

- `absOriA` -> `absoluteOrientationAlpha`
- `absOriB` -> `absoluteOrientationBeta`
- `absOriG` -> `absoluteOrientationGamma`
- `absOriX` -> `absoluteOrientationBeta`
- `absOriY` -> `absoluteOrientationGamma`
- `absOriZ` -> `absoluteOrientationAlpha`
- `absoluteOrientationX` -> `absoluteOrientationBeta`
- `absoluteOrientationY` -> `absoluteOrientationGamma`
- `absoluteOrientationZ` -> `absoluteOrientationAlpha`
- `accX` -> `accelerationX`
- `accY` -> `accelerationY`
- `accZ` -> `accelerationZ`
- `att` -> `attack`
- `bandq` -> `bpq`
- `bb` -> `byteBeatExpression`
- `bbexpr` -> `byteBeatExpression`
- `bbst` -> `byteBeatStartTime`
- `bp` -> `bpf`
- `bpa` -> `bpattack`
- `bpd` -> `bpdecay`
- `bpdepthfreq` -> `bpdepthfrequency`
- `bpe` -> `bpenv`
- `bpr` -> `bprelease`
- `bps` -> `bpsustain`
- `ch` -> `channels`
- `chunkback` -> `chunkBack`
- `chunkbackinto` -> `chunkBackInto`
- `chunkinto` -> `chunkInto`
- `colour` -> `color`
- `ctf` -> `lpf`
- `datt` -> `duckattack`
- `dec` -> `decay`
- `delayfb` -> `delayfeedback`
- `delays` -> `delaysync`
- `delayt` -> `delaytime`
- `det` -> `detune`
- `dfb` -> `delayfeedback`
- `dist` -> `distort`
- `disttype` -> `distorttype`
- `distvol` -> `distortvol`
- `ds` -> `delaysync`
- `dt` -> `delaytime`
- `duck` -> `duckorbit`
- `duckatt` -> `duckattack`
- `duckons` -> `duckonset`
- `dur` -> `duration`
- `echowith` -> `echoWith`
- `eish` -> `euclidish`
- `fTrans` -> `ftranspose`
- `fTranspose` -> `ftranspose`
- `fastcat` -> `seq`
- `fastchunk` -> `fastChunk`
- `fastgap` -> `fastGap`
- `flux` -> `juxFlip`
- `fluxBy` -> `juxFlipBy`
- `fluxby` -> `juxFlipBy`
- `fm` -> `fmi`
- `fmatt` -> `fmattack`
- `fmdec` -> `fmdecay`
- `fme` -> `fmenv`
- `fmrel` -> `fmrelease`
- `fmsus` -> `fmsustain`
- `ftrans` -> `ftranspose`
- `getDur` -> `getDuration`
- `gravX` -> `gravityX`
- `gravY` -> `gravityY`
- `gravZ` -> `gravityZ`
- `hp` -> `hpf`
- `hpa` -> `hpattack`
- `hpd` -> `hpdecay`
- `hpdepthfreq` -> `hpdepthfrequency`
- `hpe` -> `hpenv`
- `hpr` -> `hprelease`
- `hps` -> `hpsustain`
- `hresonance` -> `hpq`
- `inv` -> `invert`
- `ir` -> `irbegin`, `iresponse`
- `iterback` -> `iterBack`
- `juxby` -> `juxBy`
- `juxflip` -> `juxFlip`
- `juxflipby` -> `juxFlipBy`
- `legato` -> `clip`
- `loopb` -> `loopBegin`
- `loope` -> `loopEnd`
- `lp` -> `lpf`
- `lpa` -> `lpattack`
- `lpd` -> `lpdecay`
- `lpdepthfreq` -> `lpdepthfrequency`
- `lpe` -> `lpenv`
- `lpr` -> `lprelease`
- `lps` -> `lpsustain`
- `o` -> `orbit`
- `oriA` -> `orientationAlpha`
- `oriB` -> `orientationBeta`
- `oriG` -> `orientationGamma`
- `oriX` -> `orientationBeta`
- `oriY` -> `orientationGamma`
- `oriZ` -> `orientationAlpha`
- `orientationX` -> `orientationBeta`
- `orientationY` -> `orientationGamma`
- `orientationZ` -> `orientationAlpha`
- `patt` -> `pattack`
- `pdec` -> `pdecay`
- `perCycle` -> `per`
- `ph` -> `phaser`
- `phasdp` -> `phaserdepth`
- `phc` -> `phasercenter`
- `phd` -> `phaserdepth`
- `phs` -> `phasersweep`
- `pickSqueeze` -> `inhabit`
- `pickmodSqueeze` -> `inhabitmod`
- `plyforeach` -> `plyForEach`
- `plywith` -> `plyWith`
- `pm` -> `polymeter`
- `polyrhythm` -> `stack`
- `pr` -> `stack`
- `prel` -> `prelease`
- `punchcard` -> `pianoroll`
- `pwr` -> `pwrate`
- `pws` -> `pwsweep`
- `randcat` -> `chooseCycles`
- `rdim` -> `roomdim`
- `rel` -> `release`
- `resonance` -> `lpq`
- `rfade` -> `roomfade`
- `rib` -> `ribbon`
- `rlp` -> `roomlp`
- `rotA` -> `rotationAlpha`
- `rotB` -> `rotationBeta`
- `rotG` -> `rotationGamma`
- `rotX` -> `rotationBeta`
- `rotY` -> `rotationGamma`
- `rotZ` -> `rotationAlpha`
- `rotationX` -> `rotationBeta`
- `rotationY` -> `rotationGamma`
- `rotationZ` -> `rotationAlpha`
- `rsize` -> `roomsize`
- `scaleTrans` -> `scaleTranspose`
- `seg` -> `segment`
- `size` -> `roomsize`
- `slowChunk` -> `chunk`
- `slowchunk` -> `chunk`
- `sound` -> `s`
- `sparsity` -> `slow`
- `src` -> `source`
- `strans` -> `scaleTranspose`
- `stutWith` -> `echoWith`
- `stutwith` -> `echoWith`
- `sus` -> `sustain`
- `sz` -> `roomsize`
- `timeCat` -> `stepcat`
- `trans` -> `transpose`
- `trem` -> `tremolo`
- `tremdepth` -> `tremolodepth`
- `tremphase` -> `tremolophase`
- `tremshape` -> `tremoloshape`
- `tremskew` -> `tremoloskew`
- `tremsync` -> `tremolosync`
- `tscope` -> `scope`
- `v` -> `vib`
- `vel` -> `velocity`
- `vibrato` -> `vib`
- `vmod` -> `vibmod`
- `warpatt` -> `warpattack`
- `warpdec` -> `warpdecay`
- `warprel` -> `warprelease`
- `warpsus` -> `warpsustain`
- `wavetablePhaseRand` -> `wtphaserand`
- `wavetablePosition` -> `wt`
- `wavetableWarp` -> `warp`
- `wavetableWarpMode` -> `warpmode`
- `wrandcat` -> `wchooseCycles`
- `wtatt` -> `wtattack`
- `wtdec` -> `wtdecay`
- `wtrel` -> `wtrelease`
- `wtsus` -> `wtsustain`
