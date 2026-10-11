# strudel functions guide

strudel is a JavaScript live-coding language for writing music patterns, evaluated in the browser REPL.

This file is a hands-on tutorial for an LLM agent operating the strudel REPL (humans are welcome too): the
pattern-writing concepts, the mini-notation language, and the usage context around the REPL's functions, in
the order the project teaches them. It is generated from the project's tutorial pages under
`website/src/pages`, one `##` section per page, with the doc.json entry for every symbol a page
discusses spliced in where the page uses it.

How this file relates to `API-REFERENCE.md`:

- This file is the narrative: how pattern code is written and what the functions are for, with examples in
  context. Its symbol entries are rendered from the same doc.json, so a symbol documented here and in the
  reference is described identically.
- `API-REFERENCE.md` is the exhaustive reference: every callable symbol grouped by package, alphabetical,
  with signatures and examples. It only documents symbols that carry JSDoc comments.
- The two overlap deliberately. To check whether a function exists, what it is called and what arguments it
  takes, use `API-REFERENCE.md`; to learn how to write pattern code and which functions go together, use
  this file.

Mini-notation IS covered here, in the `Mini-notation` section: the `"bd hh*2"` pattern language, its
`*` `/` `!` `?` `|` `@` operators, the `~` and `-` rests, `,` for parallel patterns, and the `.` / `:`
shorthands (separators such as `s("bd:2")` set the sample index `n`, not a speed). Mini-notation has no
JSDoc entries, so `API-REFERENCE.md` cannot describe it and points at these pages instead.

Structure: `##` is one tutorial page and deeper headings are its sections, with each symbol's entry under
the heading the page wrote for it. Inside an entry, `**Parameters**`, `**Returns**` and `**Examples**` are
labelled blocks, and an entry marked "Pattern method" is chained onto a pattern (`"bd".fast(2)`). Names in
`Backticks` are symbols or code; code is in plain ```js fences. There are no links, anchors or images: a
link from the original page appears as its label, and an external URL as `label (url)` plain text.

Generated file - do not edit by hand. Regenerate with `node jsdoc/generate-guide-md.mjs`.

## Pattern Functions

Let's learn all about functions to create and modify patterns.
At the core of Strudel, everything is made of functions.

For example, everything you can do with the Mini-Notation can also be done with a function.
This Pattern in Mini Notation:

```js
note("c3 eb3 g3")
```

is equivalent to this Pattern without Mini Notation:

```js
note(seq("c3", "eb3", "g3"))
```

Similarly, there is an equivalent function for every aspect of the mini notation.

Which representation to use is a matter of context. As a rule of thumb, functions
are better suited in a larger context, while mini notation is more practical for individual rhythms.

### Limits of Mini Notation

While the Mini Notation is a powerful way to write rhythms concisely, it also has its limits. Take this example:

```js
stack(
  note("c2 eb2(3,8)").s('sawtooth').cutoff(800),
  s("bd(5,8), hh*8")
)
```

Here, we are using mini notation for the individual rhythms, while using the function `stack` to mix them.
While stack is also available as `,` in mini notation, we cannot use it here, because we have different types of sounds.

### Combining Patterns

You can freely mix JS patterns, mini patterns and values! For example, this pattern:

```js
cat(
  stack("g3","b3","e4"),
  stack("a3","c3","e4"),
  stack("b3","d3","fs4"),
  stack("b3","e4","g4")
).note()
```

...is equivalent to:

```js
cat(
  "g3,b3,e4",
  "a3,c3,e4",
  "b3,d3,f#4",
  "b3,e4,g4"
).note()
```

... as well as:

```js
note("<[g3,b3,e4] [a3,c3,e4] [b3,d3,f#4] [b3,e4,g4]>")
```

While mini notation is almost always shorter, it only has a handful of modifiers: * / ! @.
When using JS patterns, there is a lot more you can do.

Next, let's look at how you can create patterns

## Creating Patterns

The following functions will return a pattern.
These are the equivalents used by the Mini Notation:

| function                       | mini             |
| ------------------------------ | ---------------- |
| `cat(x, y)`                    | `"<x y>"`        |
| `seq(x, y)`                    | `"x y"`          |
| `stack(x, y)`                  | `"x,y"`          |
| `stepcat([3,x],[2,y])`         | `"x@3 y@2"`      |
| `polymeter([a, b, c], [x, y])` | `"{a b c, x y}"` |
| `polymeterSteps(2, x, y, z)`   | `"{x y z}%2"`    |
| `silence`                      | `"~"`            |

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

### polymeterSteps

`polymeterSteps` is documented on the website but has no doc.json entry; see API-REFERENCE.md.

### silence

`silence` — a Pattern value, not a function. Use it directly, e.g. `silence.range(...)`.

Does absolutely nothing..

**Examples**

```js
silence // "~"
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

After Pattern Constructors, let's see what Time Modifiers are available.

## Time Modifiers

The following functions modify a pattern temporal structure in some way.
Some of these have equivalent operators in the Mini Notation:

| function               | mini         |
| ---------------------- | ------------ |
| `"x".slow(2)`          | `"x/2"`      |
| `"x".fast(2)`          | `"x*2"`      |
| `"x".euclid(3,8)`      | `"x(3,8)"`   |
| `"x".euclidRot(3,8,1)` | `"x(3,8,1)"` |

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

#### euclidRot

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

#### euclidLegato

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

### rev

`rev() -> Pattern`

Pattern method — call it on a pattern: `pattern.rev(...)`.

Reverse all cycles in a pattern. See also `revv` for reversing a whole pattern.

**Examples**

```js
note("c d e g").rev()
```

### palindrome

`palindrome()`

Applies `rev` to a pattern every other cycle, so that the pattern alternates between forwards and backwards.

**Examples**

```js
note("c d e g").palindrome()
```

### iter

`iter() -> Pattern`

Pattern method — call it on a pattern: `pattern.iter(...)`.

Divides a pattern into a given number of subdivisions, plays the subdivisions in order, but increments the starting subdivision each cycle. The pattern wraps to the first subdivision after the last subdivision is played.

**Examples**

```js
note("0 1 2 3".scale('A minor')).iter(4)
```

#### iterBack

`iterBack() -> Pattern`

Pattern method — call it on a pattern: `pattern.iterBack(...)`.

Synonyms: iterback

Like `iter`, but plays the subdivisions in reverse order. Known as iter' in tidalcycles

**Examples**

```js
note("0 1 2 3".scale('A minor')).iterBack(4)
```

### ply

`ply()`

The ply function repeats each event the given number of times.

**Examples**

```js
s("bd ~ sd cp").ply("<1 2 3>")
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

### zoom

`zoom()`

Plays a portion of a pattern, specified by the beginning and end of a time span. The new resulting pattern is played over the time period of the original pattern:

**Examples**

```js
s("bd*2 hh*3 [sd bd]*2 perc").zoom(0.25, 0.75)
// s("hh*3 [sd bd]*2") // equivalent
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

### fastGap

`fastGap()`

Synonyms: fastgap

speeds up a pattern like fast, but rather than it playing multiple times as fast would it instead leaves a gap in the remaining space of the cycle. For example, the following will play the sound pattern "bd sn" only once but compressed into the first half of the cycle, i.e. twice as fast.

**Examples**

```js
s("bd sd").fastGap(2)
```

### inside

`inside()`

Carries out an operation 'inside' a cycle.

**Examples**

```js
"0 1 2 3 4 3 2 1".inside(4, rev).scale('C major').note()
// "0 1 2 3 4 3 2 1".slow(4).rev().fast(4).scale('C major').note()
```

### outside

`outside()`

Carries out an operation 'outside' a cycle.

**Examples**

```js
"<[0 1] 2 [3 4] 5>".outside(4, rev).scale('C major').note()
// "<[0 1] 2 [3 4] 5>".fast(4).rev().slow(4).scale('C major').note()
```

### cpm

`cpm()`

**Deprecated**: still available in the REPL, but prefer the current alternative.

Plays the pattern at the given cycles per minute.

**Examples**

```js
s("<bd sd>,hh*2").cpm(90) // = 90 bpm
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

Apart from modifying time, there are ways to Control Parameters.

## Control Parameters

Besides functions that control time, we saw earlier that functions like `note` and `cutoff` control different parameters (short params) of an event.
Let's now look more closely at how these `param(eter) functions` work.

### Parameter Functions

A very powerful feature of tidal patterns is that each parameter can be controlled independently:

```js
note("c a f e")
.cutoff("<500 1000 2000 [4000 8000]>")
.gain(.8)
.s('sawtooth')
.log()
```

In this example, the parameters `note`, `cutoff`, `gain` and `s` are controlled independently by either patterns or plain values (numbers / text).
After pressing play, we can observe the time and parameter values of each event (hap) in the output created by `.log()`.

#### Plain vs Parameterized Values

Patterns that are not wrapped inside a param function will contain unlabeled `plain values`:

```js
"<c e g>".log()
```

This will not generate any sound output, because Strudel could only guess which param is meant by these letters.

Now compare that to the version wrapped in `note`:

```js
note("<c e g>").log()
```

Now it is clear that these letters are meant to be played as notes.
Under the hood, the `note` function (as well as all other param functions)
will wrap each plain value in an object. If the note function did not exist, we would need to write:

```js
cat({note:'c'},{note:'e'},{note:'g'}).log()
```

This will have the same output, though it is rather unwieldy to read and write.

#### Wrapping Parameter Functions

To avoid too much nesting, param functions can also be chained like this:

```js
cat('c', 'e', 'g').note().log()
```

This is equivalent to `note(cat('c','e','g')).log()`.

You can use this with any function that declares a type (like `n`, `s`, `note`, `freq` etc), just make sure to leave the parens empty!

#### Plain Value Modification

Patterns of plain values can be modified with any of the following operators:

```js
"50 60 70".add("<0 1 2>").log()
```

Here, the add function modifies the numbers on the left.
Again, there is no output because these numbers have no meaning without a param.

#### Param Value Modification

To modify a parameter value, you can either:

- Use the operator on the plain value pattern, inside the param function:

```js
note("50 60 70".add("<0 1 2>")).room(.1).log()
```

- Similarly, use the operator on the plain value pattern and wrap it later:

```js
"50 60 70".add("<0 1 2>").note().room(.1).log()
```

- Specify which param should be modified inside the operator function:

```js
note("50 60 70").room(.1).add(note("<0 1 2>")).log()
```

Remember the execution of the chained functions goes from left to right.

### Operators

This group of functions allows to modify the value of events.

#### add

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

#### sub

`sub()`

Pattern method — call it on a pattern: `pattern.sub(...)`.

Like add, but the given numbers are subtracted.

**Examples**

```js
n("0 2 4".sub("<0 1 2 3>")).scale("C4:minor")
// See add for more information.
```

#### mul

`mul()`

Pattern method — call it on a pattern: `pattern.mul(...)`.

Multiplies each number by the given factor.

**Examples**

```js
"<1 1.5 [1.66, <2 2.33>]>*4".mul(150).freq()
```

#### div

`div()`

Pattern method — call it on a pattern: `pattern.div(...)`.

Divides each number by the given factor.

#### round

`round() -> Pattern`

Pattern method — call it on a pattern: `pattern.round(...)`.

Assumes a numerical pattern. Returns a new pattern with all values rounded
to the nearest integer.

**Examples**

```js
n("0.5 1.5 2.5".round()).scale("C:major")
```

#### floor

`floor() -> Pattern`

Pattern method — call it on a pattern: `pattern.floor(...)`.

Assumes a numerical pattern. Returns a new pattern with all values set to
their mathematical floor. E.g. `3.7` replaced with to `3`, and `-4.2`
replaced with `-5`.

**Examples**

```js
note("42 42.1 42.5 43".floor())
```

#### ceil

`ceil() -> Pattern`

Pattern method — call it on a pattern: `pattern.ceil(...)`.

Assumes a numerical pattern. Returns a new pattern with all values set to
their mathematical ceiling. E.g. `3.2` replaced with `4`, and `-4.2`
replaced with `-4`.

**Examples**

```js
note("42 42.1 42.5 43".ceil())
```

#### range

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

#### rangex

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

#### range2

`range2() -> Pattern`

Pattern method — call it on a pattern: `pattern.range2(...)`.

Assumes a numerical pattern, containing bipolar values in the range -1 .. 1
Returns a new pattern with values scaled to the given min/max range.

**Examples**

```js
s("[bd sd]*2,hh*8")
.cutoff(sine2.range2(500,4000))
```

#### ratio

`ratio() -> Pattern`

Pattern method — call it on a pattern: `pattern.ratio(...)`.

Allows dividing numbers via list notation using ":".
Returns a new pattern with just numbers.

**Examples**

```js
ratio("1, 5:4, 3:2").mul(110)
.freq().s("piano")
```

#### as

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

### Custom Parameters

You can also create your own parameters:

```js
let x = createParam('x')
x(sine.range(0, 200))
```

Multiple params can also be created in a more consice way, using `createParams`:

```js
let { x, y } = createParams('x', 'y');
x(sine.range(0, 200)).y(cosine.range(0, 200));
```

Note that these params will not do anything until you give them meaning in your custom output!

From modifying parameters we transition to the concept of Signals.

## Continuous Signals

Signals are patterns with continuous values, meaning they have theoretically infinite steps.
They can provide streams of numbers that can be sampled at discrete points in time.

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

### sine

`sine` — a Pattern value, not a function. Use it directly, e.g. `sine.range(...)`.

A sine signal between 0 and 1.

**Examples**

```js
n(sine.segment(16).range(0,15))
.scale("C:minor")
```

### cosine

`cosine` — a Pattern value, not a function. Use it directly, e.g. `cosine.range(...)`.

A cosine signal between 0 and 1.

**Examples**

```js
n(stack(sine,cosine).segment(16).range(0,15))
.scale("C:minor")
```

### tri

`tri` — a Pattern value, not a function. Use it directly, e.g. `tri.range(...)`.

A triangle signal between 0 and 1.

**Examples**

```js
n(tri.segment(8).range(0,7)).scale("C:minor")
```

### square

`square` — a Pattern value, not a function. Use it directly, e.g. `square.range(...)`.

A square signal between 0 and 1.

**Examples**

```js
n(square.segment(4).range(0,7)).scale("C:minor")
```

### rand

`rand` — a Pattern value, not a function. Use it directly, e.g. `rand.range(...)`.

A continuous pattern of random numbers, between 0 and 1.

**Examples**

```js
// randomly change the cutoff
s("bd*4,hh*8").cutoff(rand.range(500,8000))
```

### Ranges from -1 to 1

There is also `saw2`, `sine2`, `cosine2`, `tri2`, `square2` and `rand2` which have a range from -1 to 1!

### perlin

`perlin` — a Pattern value, not a function. Use it directly, e.g. `perlin.range(...)`.

Generates a continuous pattern of perlin noise (https://en.wikipedia.org/wiki/Perlin_noise), in the range 0..1.

**Examples**

```js
// randomly change the cutoff
s("bd*4,hh*8").cutoff(perlin.range(500,8000))
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

Next up: Random Modifiers

## Random Modifiers

These methods add random behavior to your Patterns.

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

### someCycles

`someCycles() -> Pattern`

Pattern method — call it on a pattern: `pattern.someCycles(...)`.

Shorthand for `.someCyclesBy(0.5, fn)`

**Examples**

```js
s("bd,hh*8").someCycles(x=>x.speed("0.5"))
```

### often

`often() -> Pattern`

Pattern method — call it on a pattern: `pattern.often(...)`.

Shorthand for `.sometimesBy(0.75, fn)`

**Examples**

```js
s("hh*8").often(x=>x.speed("0.5"))
```

### rarely

`rarely() -> Pattern`

Pattern method — call it on a pattern: `pattern.rarely(...)`.

Shorthand for `.sometimesBy(0.25, fn)`

**Examples**

```js
s("hh*8").rarely(x=>x.speed("0.5"))
```

### almostNever

`almostNever() -> Pattern`

Pattern method — call it on a pattern: `pattern.almostNever(...)`.

Shorthand for `.sometimesBy(0.1, fn)`

**Examples**

```js
s("hh*8").almostNever(x=>x.speed("0.5"))
```

### almostAlways

`almostAlways() -> Pattern`

Pattern method — call it on a pattern: `pattern.almostAlways(...)`.

Shorthand for `.sometimesBy(0.9, fn)`

**Examples**

```js
s("hh*8").almostAlways(x=>x.speed("0.5"))
```

### never

`never() -> Pattern`

Pattern method — call it on a pattern: `pattern.never(...)`.

Shorthand for `.sometimesBy(0, fn)` (never calls fn)

**Examples**

```js
s("hh*8").never(x=>x.speed("0.5"))
```

### always

`always() -> Pattern`

Pattern method — call it on a pattern: `pattern.always(...)`.

Shorthand for `.sometimesBy(1, fn)` (always calls fn)

**Examples**

```js
s("hh*8").always(x=>x.speed("0.5"))
```

Next up: Conditional Modifiers

## Conditional Modifiers

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

#### chunkBack

`chunkBack() -> Pattern`

Pattern method — call it on a pattern: `pattern.chunkBack(...)`.

Synonyms: chunkback

Like `chunk`, but cycles through the parts in reverse order. Known as chunk' in tidalcycles

**Examples**

```js
"0 1 2 3".chunkBack(4, x=>x.add(7))
.scale("A:minor").note()
```

#### fastChunk

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

### mask

`mask()`

Pattern method — call it on a pattern: `pattern.mask(...)`.

Returns silence when mask is 0 or "~"

**Examples**

```js
note("c [eb,g] d [eb,g]").mask("<1 [0 1]>")
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

### invert

`invert()`

Synonyms: inv

Swaps 1s and 0s in a binary pattern.

**Examples**

```js
s("bd").struct("1 0 0 1 0 0 1 0".lastOf(4, invert))
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

### pickmod

`pickmod(pat: Pattern, xs: *) -> Pattern`

The same as `pick`, but if you pick a number greater than the size of the list,
it wraps around, rather than sticking at the maximum value.
For example, if you pick the fifth pattern of a list of three, you'll get the
second one.

**Parameters**

- `pat` (`Pattern`)
- `xs` (`*`)

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

### pickmodF

`pickmodF(pat: Pattern, lookup: Pattern, lookup: Function[]|Object) -> Pattern`

The same as `pickF`, but if you pick a number greater than the size of the functions list,
it wraps around, rather than sticking at the maximum value.

**Parameters**

- `pat` (`Pattern`)
- `lookup` (`Pattern`): a pattern of indices or names
- `lookup` (`Function[]|Object`): the array or lookup object of functions from which to pull

### pickRestart

`pickRestart(pat: Pattern, xs: *) -> Pattern`

Similar to `pick`, but the choosen pattern is restarted when its index is triggered.

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

### pickReset

`pickReset(pat: Pattern, xs: *) -> Pattern`

Similar to `pick`, but the choosen pattern is reset when its index is triggered.

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

After Conditional Modifiers, let's see what Accumulation Modifiers have to offer.

## Accumulation Modifiers

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

There are also Tonal Functions.

## Low frequency oscillators (LFO)

A low frequency oscillator (or short LFO) is a common way on synthesizers to continuously modulate various signals.

This documentation is an interactive version of glossing's tutorial https://www.youtube.com/watch?v=11frBA9L638

### Signals vs LFOs

In Strudel, there are two ways to modulate:

1. signals for pattern-level modulation
2. `lfo` (this page) for audio-level modulation

### Applying an LFO

Here, the LFO will change the frequency of the `saw`. Put a comment like this `//.lfo()` to see and hear how it changes, and remove the comment again.

```js
s("saw").lfo()
  .lpf(800)
  ._spectrum({height: 300, width: 800})
```

By default, the LFO will modulate the control parameter which is right before `.lfo()`:

```js
s("saw")
  .lpf(800).lfo()
  ._spectrum({height: 300, width: 800})
```

Here, the LFO will modulate the low pass filter `.lpf`.

### Moving away from the default

The following sections explain how to pass parameters to `.lfo`. Similar to `._spectrum` above, almost all the configuration of `lfo` lives inside a json object, starting with `{` and ending with `}`.
All the parameters (except `id`) are written as `key: value` inside and separated by `,`.

The reference refers to them as `config.key`, i.e. for the following one as `config.control` but you use them like below.

### Control

`control` determines which parameter will be modulated. This allows you to place your `lfo` at different places,
not necessarily immediately after the controlled parameter.

Here, we place `lfo` after the `s`, but it modulates the low pass filter.

```js
s("saw")
  .lfo({control:'lpf'})
  .lpf(800)
  ._spectrum({height: 300, width: 800})
```

You can even influence parameters which are always present, even if you haven't explicitly written them, like `gain`.

```js
s("saw")
  .lfo({control:'gain'})
  .lpf(800)
  ._spectrum({height: 300, width: 800})
```

`control` has an alias `c`.

### Rate

`rate` determines how often the `lfo` oscillates per second:

```js
 s("saw")
  .lfo({c:'gain', rate:"<2 4>"})
  .lpf(800)
  ._spectrum({height: 300, width: 800})
```

The alias of `rate` is `r`.

### Sync

Instead of controlling the frequency with `rate` by setting a frequency in Hz, you can use `sync`
to snychronize your lfo with your other patterns.

`sync` expresses this frequency as "times per cycle"

Try removing the `sync: "<2 4 8 0.5 >"` from the pattern and notice that there is something not in sync.

```js
$: s("bd*4").bank("TR909").postgain(0.5)
$: s("saw")
  .lpf(800)
  .lfo({sync: "<2 4 8 0.5 >"})
  ._spectrum({height: 300, width: 800})
```

You can put patterns into the parameters of `lfo` if you want them to change over time, as seen above.

### Relative Depth

`.depth` is relative depth, a value of 1 (the default) means that
the value goes above and below by half of the value which is being modulated

E.g. for depth `1`, it modulates the frequency to oscillate between 32 = (64/2) and 96 (= 64 + 64/2).

```js
s("saw").freq(64)
  .lfo({r: 2, depth: "<1 2 3>"})
  .lpf(800)
  ._spectrum({height: 300, width: 800})
```

The `freq` is not needed here, as this is the default frequency, it's just for instructive purposes.

The aliases of `depth` are `dr` and `dep`.

### Absolute Depth

`depthabs` controls the absolute modulation depth. For example you can modulate
the low pass filter by exactly 250 Hz up and below:

```js
s("saw")
  .lpf(800)
  .lfo({r: 2, depthabs: "250"})
```

The alias of `depthabs` is `da`.

### DC offset

If you don't want to go up or down by the same amount with your modulation, then you can shift the center of the modulation with `dcoffset`.
The default value is -0.5, which is the middle point between:
"dcoffset = 0: All modulations increase the control parameter (or keep it constant)"
"dcoffset = -1: All modulations decrease the control parameter (or keep it constant)"

Other values will work as well.

```js
s("saw")
  .lpf(600)
  .lfo({r: 2, da: "200", dcoffset: "<-0.5 -1 -0.5 0>"})
```

The marvellous alias of `dcoffset` is `dc`.

### Shape

You can change the shape of the modulation with `shape`. The default is `triangle`, but other shapes are available too:

```js
s("saw")
  .lpf(800)
  .lfo({r: 2, shape: "<triangle sine ramp saw square>"})
```

You can add `._spectrum()` to see the shape of the modulations in the spectrum.

You can also get these shapes by using numbers:

| Shape    | number |
| -------- | ------ |
| triangle | 0      |
| sine     | 1      |
| ramp     | 2      |
| saw      | 3      |
| square   | 4      |

this way, you can use a function like `irand` to generate numbers:

```js
s("saw")
  .lpf(800)
  .lfo({r: 2, shape: irand(4)})
```

The alias of `shape` is `sh`.

### Skewing some shapes

You can influence some of the shapes (`triangle` and `square`) in more detail.

The default skew is 0.5, it does different things for these two.

For `triangle`, it skews the top of the triangle to the left or right,
where 0 makes it look like `saw` and 1 makes it look like `ramp`.

For `square`, the skew influences the pulse width (see reference for `pulse` and `.pw`):

```js
s("saw")
  .lpf(800)
  .lfo({r: 2, sh: "<triangle square>/5",
    skew: "<0 0.25 0.5 0.75 1>"})
```

The alias of `skew` is `sk`.

### Curve

You can change the curves of your lfo and can make it more intense.
The default value is 1. Writing numbers greater than one can make it more intense,
numbers between 0 and 1 will make it less intense.
This will raise the lfo to the power of curve, so larger numbers (such as 10) can have very unexpected results.

```js
s("saw")
  .lpf(800)
  .lfo({r: 2, sh: "<triangle>", curve: "<1.3 1 0.75 1>"})
```

### Referencing your lfos with id

All the lfos are numbered from the first starting with 0, the second having an `id` of 1 and so on.

You can refer to this in a later call if you want to modify a specific `lfo`.

The `id` is outside of the config json (which is different from the other parameters of `lfo()`

Try out how the sound changes when you replace the 0 with a 1.

```js
s("saw").lfo().lpf(800)
.lfo({s: "<4 8 0.5>"})
.sometimes(x => x.lfo({dr: "4"},0))
```

You can also name your lfos and refer to them by name, using the `id` parameter

```js
s("saw").lfo({}, "lfo_freq_saw").lpf(800)
.lfo({s: "<4 8 0.5>"}, "lfo_lpf")
.sometimes(x => x.lfo({dr: "4"},"<lfo_lpf lfo_freq_saw>"))
```

### FX index

If you are using `FX()` to reorder your effects, you dont need to write your lfos inside the `FX`,
but instead can refer to them by their FX index (starting with 0)

```js
s("saw").FX(
  distort(3),
  gain(0.3), // this has the fx index 1
  lpf(400)
).lfo({s: 16, dr:2, c:"gain", fxi: 1})
```

### Modulating other LFOs with Sub-control

LFOs can modulate other lfos and will modulate their frequency (given by `r` or `s`):

```js
s("saw").lpf(400).gain(0.8)
  .lfo({s: 16, dr:2, c:"gain"})
  .lfo({s: 0.3, dc:-1, dr: 0.8})
```

To modulate other parameters of the first lfo (like `skew`, `depth` and so on), we can specify this with `subControl`or
its alias `sc`

```js
s("saw").lpf(400).gain(0.8)
  .lfo({s: 4, dr:2, c:"gain"})
.lfo({s: 0.3, sc: "skew"})
```

## Tonal Functions

These functions use tonaljs (https://github.com/tonaljs/tonal) to provide helpers for musical operations.

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

Here's an example of how you can play chords and a bassline:

```js
chord("<C^7 A7b13 Dm7 G7>*2")
  .dict('ireal').layer(
  x=>x.struct("[~ x]*2").voicing()
  ,
  x=>n("0*4").set(x).mode("root:g2").voicing()
  .s('sawtooth').cutoff("800:4:2")
)
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

### transpose(semitones)

Transposes all notes to the given number of semitones:

```js
"[c2 c3]*4".transpose("<0 -2 5 3>").note()
```

This method gets really exciting when we use it with a pattern as above.

Instead of numbers, scientific interval notation can be used as well:

```js
"[c2 c3]*4".transpose("<1P -2M 4P 3m>").note()
```

### scaleTranspose(steps)

Transposes notes inside the scale by the number of steps:

```js
"[-8 [2,4,6]]*2"
.scale('C4 bebop major')
.scaleTranspose("<0 -1 -2 -3 -4 -5 -6 -4>*2")
.note()
```

### rootNotes(octave = 2)

Turns chord symbols into root notes of chords in given octave.

```js
"<C^7 A7b13 Dm7 G7>*2".rootNotes(3).note()
```

Together with layer, struct and voicings, this can be used to create a basic backing track:

```js
"<C^7 A7b13 Dm7 G7>*2".layer(
  x => x.voicings('lefthand').struct("[~ x]*2").note(),
  x => x.rootNotes(2).note().s('sawtooth').cutoff(800)
)
```

## Stepwise patterning (experimental)

This is a developing area of strudel, and behaviour might change or be renamed in future versions. Feedback and ideas are welcome!

### Introduction

Usually in strudel, the only reference point for most pattern transformations is the _cycle_. Now it is possible to also work with _steps_, via a growing range of functions.

For example usually when you `fastcat` two patterns together, the cycles will be squashed into half a cycle each:

```js
fastcat("bd hh hh", "bd hh hh cp hh").sound()
```

With the new stepwise `stepcat` function, the steps of the two patterns will be evenly distributed across the cycle:

```js
stepcat("bd hh hh", "bd hh hh cp hh").sound()
```

By default, steps are counted according to the 'top level' in mini-notation. For example `"a [b c] d e"` has five events in it per cycle, but is counted as four steps, where `[b c]` is counted as a single step.

However, you can mark a different metrical level to count steps relative to, using a `^` at the start of a sub-pattern. If we do this to the subpattern in our example: `"a [^b c] d e"`, then the pattern is now counted as having _eight_ steps. This is because 'b' and 'c' are each counted as single steps, and the events in the pattern are twice as long, and so counted as two steps each.

### Pacing the steps

Some stepwise functions don't appear to do very much on their own, for example these two examples of the `expand` function sound exactly the same despite being expanded by different amounts:

```js
"c a f e".expand(2).note().sound("folkharp")
```

```js
"c a f e".expand(4).note().sound("folkharp")
```

The number of steps per cycle is being changed behind the scenes, but on its own, that doesn't do anything. You will hear a difference however, once you use another stepwise function with it, for example `stepcat`:

```js
stepcat("c a f e".expand(2), "g d").note()
  .sound("folkharp")
```

```js
stepcat("c a f e".expand(4), "g d").note()
  .sound("folkharp")
```

You should be able to hear that `expand` increases the duration of the steps of the first subpattern, proportionally to the second one.

You can also change the speed of a pattern to match a given number of steps per cycle, with the `pace` function:

```js
stepcat("c a f e".expand(2), "g d").note()
  .sound("folkharp")
  .pace(8)
```

```js
stepcat("c a f e".expand(4), "g d").note()
  .sound("folkharp")
  .pace(8)
```

The first example has ten steps, and the second example has 18 steps, but are then both played a rate of 8 steps per cycle.

The argument to `expand` can also be patterned, and will be treated in a stepwise fashion. This means that the patterns from the changing values in the argument will be `stepcat`ted together:

```js
note("c a f e").sound("folkharp").expand("3 2 1 1 2 3")
```

This results in a dense pattern, because the different expanded versions are squashed into a single cycle. `pace` is again handy here for slowing down the pattern to a particular number of steps per cycle:

```js
note("c a f e").sound("folkharp").expand("3 2 1 1 2 3").pace(8)
```

Earlier versions of many of these functions had `s_` prefixes, and the `pace` function was previously known as `steps`. These still exist as aliases, but may have changed behaviour and will soon be removed. Please update your patterns!

### Stepwise functions

#### pace

`pace()`

*Experimental*

Speeds a pattern up or down, to fit to the given number of steps per cycle.

**Examples**

```js
sound("bd sd cp").pace(4)
// The same as sound("{bd sd cp}%4") or sound("<bd sd cp>*4")
```

#### stepcat

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

#### stepalt

`stepalt() -> Pattern`

*Experimental*

Concatenates patterns stepwise, according to an inferred 'steps per cycle'.
Similar to `stepcat`, but if an argument is a list, the whole pattern will alternate between the elements in the list.

**Examples**

```js
stepalt(["bd cp", "mt"], "bd").sound()
// The same as "bd cp bd mt bd".sound()
```

#### expand

`expand()`

*Experimental*

Expands the step size of the pattern by the given factor.

**Examples**

```js
sound("tha dhi thom nam").bank("mridangam").expand("3 2 1 1 2 3").pace(8)
```

#### contract

`contract()`

*Experimental*

Contracts the step size of the pattern by the given factor. See also `expand`.

**Examples**

```js
sound("tha dhi thom nam").bank("mridangam").contract("3 2 1 1 2 3").pace(8)
```

#### extend

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

#### take

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

#### drop

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

#### polymeter

`polymeter()`

Synonyms: pm

*Experimental*

Aligns the steps of the patterns, creating polymeters. The patterns are repeated until they all fit the cycle. For example, in the below the first pattern is repeated twice, and the second is repeated three times, to fit the lowest common multiple of six steps.

**Examples**

```js
// The same as note("{c eb g, c2 g2}%6")
polymeter("c eb g", "c2 g2").note()
```

#### shrink

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

#### grow

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

#### tour

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

#### zip

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

## Notes

Pitches are an important building block in many musical traditions.
In Strudel, pitches can be expressed as note names, note numbers or frequencies.
Here's the same pattern written in three different ways:

- `note`: letter notation, good for those who are familiar with western music theory:

```js
note("a3 c#4 e4 a4")
```

- `note`: number notation, good for those who want to use recognisable pitches, but don't care about music theory:

```js
note("57 61 64 69")
```

- `freq`: frequency notation, good for those who want to go beyond standardised tuning systems:

```js
freq("220 275 330 440")
```

Let's look at those in more detail...

### `note` names

Notes names can be notated with the note letter, followed by the octave number. You can notate flats with `b` and sharps with `#`.

```js
note("a3 c#4 e4 a4")
```

By the way, you can edit the contents of the player, and press "update" to hear your change!
You can also press "play" on the next player without needing to stop the last one.

### `note` numbers

If you prefer, you can also use numbers with `note` instead:

```js
note("57 61 64 69")
```

These numbers are interpreted as so called MIDI numbers (https://www.inspiredacoustics.com/en/MIDI_note_numbers_and_center_frequencies), where adjacent whole numbers are one 'semitone' apart.

You could also write decimal numbers to get 'microtonal' pitches (in between the black and white piano notes):

```js
note("74.5 75 75.5 76")
```

### `freq`

To get maximum freedom, you can also use `freq` to directly control the frequency:

```js
freq("220 275 330 440")
```

### Hearing and frequency

In the above example, we play A3 (220Hz), C#4 natural (275Hz), E4 (330Hz) and A4 (440Hz), mirroring our previous examples.

But can you hear the difference between these individual frequencies?

```js
freq("220 221 223 224")
```

How about these?

```js
freq("2020 2021 2023 2024")
```

The higher we go up...

```js
freq("5020 5021 5023 5024")
```

The less distance we can hear between the frequencies!

```js
freq("10020 10021 10023 10024")
```

Why is this? Human hearing operates logarithmically (https://www.audiocheck.net/soundtests_nonlinear.php).

### From notes to sounds

In this page, when we played a pattern of notes like this:

```js
note("a3 c#4 e4 a4")
```

We heard a simple synthesised sound, in fact we heard a triangle wave oscillator (https://en.wikipedia.org/wiki/Triangle_wave).

This is the default synthesiser used by Strudel, but how do we then make different sounds in Strudel?

Let's find out in the next page on Sounds.

## Sounds

We can play sounds with `s`, in two different ways:

- `s` can trigger audio samples, where a sound file is loaded in the background and played back:

```js
s("bd hh sd hh")
```

- `s` can trigger audio synthesisers, which are synthesised in real-time using code also running in the background:

```js
s("sawtooth square triangle sine")
```

You can learn more about both of these approaches in the pages Synths and Samples.

### Combining notes and sounds

In both of the above cases, we are no longer directly controlling the `note`/`freq` of the sound heard via `s`, as we were in the Notes page.

So how can we both control the sound and the pitch? We can _combine_ `note`/`freq` with `s` to change the sound of our pitches:

```js
note("a3 c#4 e4 a4").s("sawtooth")
```

```js
note("57 61 64 69").s("sine")
```

```js
freq("220 275 330 440").s("triangle")
```

The last example will actually sound the same with or without `s`, because `triangle` is the default value for `s`.

What about combining different notes with different sounds at the same time?

```js
freq("220 275 330 440 550").s("triangle sawtooth sine")
```

Hmm, something interesting is going on there, related to there being five notes and three sounds.

Let's now take a step back and think about the Strudel Code we've been hearing so far.

## Mini-notation

Just like Tidal Cycles (https://tidalcycles.org/), Strudel uses a so called "Mini-Notation", which is a custom language that is designed for writing rhythmic patterns using little amounts of text.

### Note

This page just explains the entirety of the Mini-Notation syntax.
If you are just getting started with Strudel, you can learn the basics of the Mini-Notation in a more practical manner in the workshop.
After that, you can come back here if you want to understand every little detail.

### Example

Before diving deeper into the details, here is a flavour of how the Mini-Notation looks like:

```js
note(`<
[e5 [b4 c5] d5 [c5 b4]]
[a4 [a4 c5] e5 [d5 c5]]
[b4 [~ c5] d5 e5]
[c5 a4 a4 ~]
[[~ d5] [~ f5] a5 [g5 f5]]
[e5 [~ c5] e5 [d5 c5]]
[b4 [b4 c5] d5 e5]
[c5 a4 a4 ~]
,
[[e2 e3]*4]
[[a2 a3]*4]
[[g#2 g#3]*2 [e2 e3]*2]
[a2 a3 a2 a3 a2 a3 b1 c2]
[[d2 d3]*4]
[[c2 c3]*4]
[[b1 b2]*2 [e2 e3]*2]
[[a1 a2]*4]
>`)
```

### Mini Notation Format

The snippet above is enclosed in backticks (`), which allows you to write multi-line strings.

You can also use regular double quotes (`"`) for single line mini-notation, as we have done already.

If you do just want to get a regular string that is _not_ parsed as mini-notation, use single quotes (`'`).

### Sequences of events in a cycle

We can play more notes by separating them with spaces:

```js
note("c e g b")
```

Here, those four notes are squashed into one cycle, so each note is a quarter second long.
Try adding or removing notes and notice how the tempo changes!

```js
note("c d e f g a b")
```

Note that the overall duration of time does not change, and instead each note length decreases.
This is a key idea, as it illustrates the 'Cycle' in TidalCycles!

Each space-separated note in this sequence is an _event_.
The time duration of each event is based on the speed or tempo of the cycle, and how many events are present.
Taking the two examples above, we have four and eight events respectively, and since they have the same cycle duration, they each have to fit their events inside the same amount of time.

This is perhaps counter-intuitive if you are used to adding notes in a sequencer or piano roll and the overall length increasing.
But, it will begin to make sense as we go through more elements of mini-notation.

### Multiplication

A sequence can be sped up by multiplying it by a number using the asterisk symbol (`*`):

```js
note("[e5 b4 d5 c5]*2")
```

The multiplication by two here means that the sequence will play twice per cycle.

Multiplications can also be decimal (`*2.75`):

```js
note("[e5 b4 d5 c5]*2.75")
```

### Division

Contrary to multiplication, division can slow the sequence down by enclosing it in brackets and dividing it by a number (`/2`):

```js
note("[e5 b4 d5 c5]/2")
```

The division by two means that the sequence will be played over the course of two cycles.
You can also use decimal numbers for any tempo you like (`/2.75`).

```js
note("[e5 b4 d5 c5]/2.75")
```

### Angle Brackets

Using angle brackets `<>`, we can define the sequence length based on the number of events:

```js
note("<e5 b4 d5 c5>")
```

The above snippet is the same as:

```js
note("[e5 b4 d5 c5]/4")
```

The advantage of the angle brackets, is that we can add more events without needing to change the number at the end.

```js
note("<e5 b4 d5 c5 e5>")
```

```js
note("<e5 b4 d5 c5 e5 b4>")
```

This is more similar to traditional music sequencers and piano rolls, where adding a note increases the perceived overall duration.
We can also play a certain number of notes per cycle by using angle brackets with multiplication:

```js
note("<e5 b4 d5 c5 a4 c5>*8")
```

Now we are playing 8 notes per cycle!

### Subdividing time with bracket nesting

To create more interesting rhythms, you can _nest_ or _enclose_ sequences (put sequences inside sequences) with brackets `[]`, like this:

Compare the difference between the following:

```js
note("e5 b4 c5 d5 c5 b4")
```

```js
note("e5 [b4 c5] d5 c5 b4")
```

```js
note("e5 [b4 c5] d5 [c5 b4]")
```

```js
note("e5 [b4 c5] d5 [c5 b4 d5 e5]")
```

```js
note("e5 [b4 c5] d5 [c5 b4 [d5 e5]]")
```

What's going on here? When we nest/enclose multiple events inside brackets (`[]`), their duration becomes the length of one event in the outer sequence.

This is a very simple change to make, but it has profound consequences.
Remember what we said earlier about how the cycles in tidal stay the same length, and the individual event lengths are divided up in this cycle?
Well, what this means is that in TidalCycles, not only can you divide time any way you want, and you can also subdivide time any way you want!

### Rests

The "~" represents a rest, and will create silence between other events:

```js
note("[b4 [~ c5] d5 e5]")
```

Alternatively, "-" can be used instead of "~". It means the same thing.

### Parallel / polyphony

Using commas, we can play chords.
The following are the same:

```js
note("[g3,b3,e4]")
```

```js
note("g3,b3,e4")
```

But to play multiple chords in a sequence, we have to wrap them in brackets:

```js
note("<[g3,b3,e4] [a3,c3,e4] [b3,d3,f#4] [b3,e4,g4]>*2")
```

### Elongation

With the "@" symbol, we can specify temporal "weight" of a sequence child:

```js
note("<[g3,b3,e4]@2 [a3,c3,e4] [b3,d3,f#4]>*2")
```

Here, the first chord has a weight of 2, making it twice the length of the other chords. The default weight is 1.

### Replication

Using "!" we can repeat without speeding up:

```js
note("<[g3,b3,e4]!2 [a3,c3,e4] [b3,d3,f#4]>*2")
```

### Randomness

Events with a "?" placed after them will have a 50% chance of being removed from the pattern:

```js
note("[g3,b3,e4]*8?")
```

Adding a number between 0 and 1 after the "?" will affect the likelihood of the event being removed. For example, events with "?0.1" placed after them will have a 10% chance of being removed:

```js
note("[g3,b3,e4]*8?0.1")
```

Events separated by a "|" will be chosen from at random:

```js
note("[g3,b3,e4] | [a3,c3,e4] | [b3,d3,f#4]")
```

### Mini-notation review

To recap what we've learned so far, compare the following patterns:

```js
note("<g3 b3 e4 [a3,c3,e4] [b3,d3,f#4]>*2")
```

```js
note("<[g3,b3,e4] [a3,c3,e4] [b3,d3,f#4]>*2")
```

```js
note("<[g3,b3,e4]/2 [a3,c3,e4] [b3,d3,f#4]>*2")
```

```js
note("<[g3,b3,e4]*2 [a3,c3,e4] [b3,d3,f#4]>*2")
```

```js
note("<[g3,b3,e4] _ [a3,c3,e4] [b3,d3,f#4]>*2")
```

```js
note("<[g3,b3,e4]@2 [a3,c3,e4] [b3,d3,f#4]>*2")
```

```js
note("<[g3,b3,e4]!2 [a3,c3,e4] [b3,d3,f#4]>*2")
```

```js
note("<[g3,b3,e4]? [a3,c3,e4] [b3,d3,f#4]>*2")
```

```js
note("<[g3|b3|e4] [a3,c3,e4] [b3,d3,f#4]>*2")
```

### Euclidian rhythms

Using round brackets after an event, we can create rhythmical sub-divisions based on three parameters: `beats`, `segments` and `offset`.
This algorithm can be found in many different types of music software, and is often referred to as a Euclidean rhythm (https://en.wikipedia.org/wiki/Euclidean_rhythm) sequencer, after computer scientist Godfriend Toussaint.
Why is it interesting? Well, consider the following simple example:

```js
s("bd(3,8,0)")
```

Sound familiar?
This is a popular Euclidian rhythm going by various names, such as "Pop Clave".
These rhythms can be found in all musical cultures, and the Euclidian rhythm algorithm allows us to express them extremely easily.
Writing this rhythm out in full require describing:

```js
s("bd ~ ~ bd ~ ~ bd ~")
```

But using the Euclidian rhythm notation, we only need to express "3 beats over 8 segments, starting on position 1".

This makes it easy to write patterns with interesting rhythmic structures and variations that still sound familiar:

```js
note("e5(2,8) b4(3,8) d5(2,8) c5(3,8)").slow(2)
```

Note that since the example above does not use the third `offset` parameter, it can be written simply as `"(3,8)"`.

```js
s("bd(3,8)")
```

Let's look at those three parameters in detail.

#### Beats

`beats`: the first parameter controls how may beats will be played.
Compare these:

```js
s("bd(2,8)")
```

```js
s("bd(5,8)")
```

```js
s("bd(7,8)")
```

#### Segments

`segments`: the second parameter controls the total amount of segments the beats will be distributed over:

```js
s("bd(3,4)")
```

```js
s("bd(3,8)")
```

```js
s("bd(3,13)")
```

#### Offsets

`offset`: the third (optional) parameter controls the starting position for distributing the beats.
We need a secondary rhythm to hear the difference:

```js
s("bd(3,8,0), hh cp")
```

```js
s("bd(3,8,3), hh cp")
```

```js
s("bd(3,8,5), hh cp")
```

### Mini-notation exercise

The most fun thing about the mini-notation, is that everything you have just learned can be combined in various ways!

Starting with this one `n`, can you make a _pattern string_ that uses every single mini-notation element above?

```js
n("60")
```

Next: How do Samples play into this?

## Samples

Samples are the most common way to make sound with tidal and strudel.
A sample is a (commonly short) piece of audio that is used as a basis for sound generation, undergoing various transformations.
Music that is based on samples can be thought of as a collage of sound. Read more about Sampling (https://en.wikipedia.org/wiki/Sampling_(music))

Strudel allows loading samples in the form of audio files of various formats (wav, mp3, ogg) from any publicly available URL.

### Default Samples

By default, strudel comes with a built-in "sample map", providing a solid base to play with.

```js
s("bd sd [~ bd] sd,hh*16, misc")
```

Here, we are using the `s` function to play back different default samples (`bd`, `sd`, `hh` and `misc`) to get a drum beat.

For drum sounds, strudel uses the comprehensive tidal-drum-machines (https://github.com/ritchse/tidal-drum-machines) library, with the following naming convention:

| Drum                 | Abbreviation |
| -------------------- | ------------ |
| Bass drum, Kick drum | bd           |
| Snare drum           | sd           |
| Rimshot              | rim          |
| Clap                 | cp           |
| Closed hi-hat        | hh           |
| Open hi-hat          | oh           |
| Crash                | cr           |
| Ride                 | rd           |
| High tom             | ht           |
| Medium tom           | mt           |
| Low tom              | lt           |

[diagram: default samples]

original von Pbroks13 (https://de.wikipedia.org/wiki/Schlagzeug#/media/Datei:Drum_set.svg)

More percussive sounds:

| Source                              | Abbreviation |
| ----------------------------------- | ------------ |
| Shakers (and maracas, cabasas, etc) | sh           |
| Cowbell                             | cb           |
| Tambourine                          | tb           |
| Other percussions                   | perc         |
| Miscellaneous samples               | misc         |
| Effects                             | fx           |

Furthermore, strudel also loads instrument samples from VCSL (https://github.com/sgossner/VCSL) by default.

To see which sample names are available, open the `sounds` tab in the REPL (https://strudel.cc/).

You can also create custom aliases for existing sounds using the `soundAlias` function:

```js
soundAlias('RolandTR808_bd', 'kick')
s("kick")
```

Note that only the sample maps (mapping names to URLs) are loaded initially, while the audio samples themselves are not loaded until they are actually played.
This behaviour of loading things only when they are needed is also called `lazy loading`.
While it saves resources, it can also lead to sounds not being audible the first time they are triggered, because the sound is still loading.
This might be fixed in the future (https://codeberg.org/uzu/strudel/issues/187)

### Sound Banks

If we open the `sounds` tab and then `drum-machines`, we can see that the drum samples are all prefixed with drum machine names: `RolandTR808_bd`, `RolandTR808_sd`, `RolandTR808_hh` etc..

We _could_ use them like this:

```js
s("RolandTR808_bd RolandTR808_sd,RolandTR808_hh*16")
```

... but thats obviously a bit much to write. Using the `bank` function, we can shorten this to:

```js
s("bd sd,hh*16").bank("RolandTR808")
```

You could even pattern the bank to switch between different drum machines:

```js
s("bd sd,hh*16").bank("<RolandTR808 RolandTR909>")
```

Behind the scenes, `bank` will just prepend the drum machine name to the sample name with `_` to get the full name.
This of course only works because the name after `_` (`bd`, `sd` etc..) is standardized.
Also note that some banks won't have samples for all sounds!

### Selecting Sounds

If we open the `sounds` tab again, followed by tab `drum machines`, there is also a number behind each name, indicating how many individual samples are available.
For example `RolandTR909_hh(4)` means there are 4 samples of a TR909 hihat available.
By default, `s` will play the first sample, but we can select the other ones using `n`, starting from 0:

```js
s("hh*8").bank("RolandTR909").n("0 1 2 3")
```

Numbers that are too high will just wrap around to the beginning

```js
s("hh*8").bank("RolandTR909").n("0 1 2 3 4 5 6 7")
```

Here, 0-3 will play the same sounds as 4-7, because `RolandTR909_hh` only has 4 sounds.

Selecting sounds also works inside the mini notation, using "`:`" like this:

```js
s("bd*4,hh:0 hh:1 hh:2 hh:3 hh:4 hh:5 hh:6 hh:7")
.bank("RolandTR909")
```

### Loading Custom Samples

You can load a non-standard sample map using the `samples` function.

#### Loading samples from file URLs

In this example we assign names `bassdrum`, `hihat` and `snaredrum` to specific audio files on a server:

```js
samples({
  bassdrum: 'bd/BT0AADA.wav',
  hihat: 'hh27/000_hh27closedhh.wav',
  snaredrum: ['sd/rytm-01-classic.wav', 'sd/rytm-00-hard.wav'],
}, 'https://raw.githubusercontent.com/tidalcycles/Dirt-Samples/master/');

s("bassdrum snaredrum:0 bassdrum snaredrum:1, hihat*16")
```

You can freely choose any combination of letters for each sample name. It is even possible to override the default sounds.
The names you pick will be made available in the `s` function.
Make sure that the URL and each sample path form a correct URL!

In the above example, `bassdrum` will load:

```txt
https://raw.githubusercontent.com/tidalcycles/Dirt-Samples/master/bd/BT0AADA.wav
|----------------------base path --------------------------------|--sample path-|
```

Note that we can either load a single file, like for `bassdrum` and `hihat`, or a list of files like for `snaredrum`!
As soon as you run the code, your chosen sample names will be listed in `sounds` -> `user`.

#### Loading Samples from a strudel.json file

The above way to load samples might be tedious to write out / copy paste each time you write a new pattern.
To avoid that, you can simply pass a URL to a `strudel.json` file somewhere on the internet:

```js
samples('https://raw.githubusercontent.com/tidalcycles/Dirt-Samples/master/strudel.json')
s("bd sd bd sd,hh*16")
```

The file is expected to define a sample map using JSON, in the same format as described above.
Additionally, the base path can be defined with the `_base` key.
The last section could be written as:

```json
{
  "_base": "https://raw.githubusercontent.com/tidalcycles/Dirt-Samples/master/",
  "bassdrum": "bd/BT0AADA.wav",
  "snaredrum": "sd/rytm-01-classic.wav",
  "hihat": "hh27/000_hh27closedhh.wav"
}
```

Please note that browsers will often cache `strudel.json` on first load, and keep using the cached
version even if the orginal has been updated. If this bites you (for example while developing a new
sample pack), you can force the browser to download a new copy by i.e. changing capitalization of one
character in the URL, or adding a URL attribute, such as:

```js
samples('https://raw.githubusercontent.com/tidalcycles/Dirt-Samples/master/strudel.json?version=2');
```

that gets ignored by GitHub (but changes the URL, forcing the browser to reload every time we increase
the version number).

It is also possible, of course, to just remove it from cache (deleting cache in browser Privacy settings,
or from the dev console if you're technically minded, or by using a cache deleting extension).

#### Generating strudel.json

You can use @strudel/sampler (https://www.npmjs.com/package/@strudel/sampler) to generate a strudel.json file for you, by running:

```sh
npx --yes @strudel/sampler --json > strudel.json
```

See other uses of strudel/sampler further below, under "From Disk via @strudel/sampler".

#### Github Shortcut

Because loading samples from github is common, there is a shortcut:

```js
samples('github:tidalcycles/dirt-samples')
s("bd sd bd sd,hh*16")
```

The format is `samples('github:<user>/<repo>/<branch>')`. If you omit `branch` (like above), the `main` branch will be used.
It assumes a `strudel.json` file to be present at the root of the repository:

```txt
https://raw.githubusercontent.com/<user>/<repo>/<branch>/strudel.json
```

#### From Disk via "Import Sounds Folder"

If you don't want to upload your samples to the internet, you can also load them from your local disk.
Go to the `sounds` tab in the REPL and open the `import-sounds` tab below the search bar.
Press the "import sounds folder" button and select a folder that contains audio files.
The folder you select can also contain subfolders with audio files.
Example:

```txt
└─ samples
   ├─ swoop
   │  ├─ swoopshort.wav
   │  ├─ swooplong.wav
   │  └─ swooptight.wav
   └─ smash
      ├─ smashhigh.wav
      ├─ smashlow.wav
      └─ smashmiddle.wav
```

In the above example the folder `samples` contains 2 subfolders `swoop` and `smash`, which contain audio files.
If you select that `samples` folder, the `user` tab (next to the `import-sounds` tab) will then contain 2 new sounds: `swoop(3) smash(3)`
The individual samples can the be played normally like `s("swoop:0 swoop:1 smash:2")`.
The samples within each sound use zero-based indexing in alphabetical order.

#### From Disk via @strudel/sampler

Instead of loading your samples into your browser with the "import sounds folder" button, you can also serve the samples from a local file server.
The easiest way to do this is using @strudel/sampler (https://www.npmjs.com/package/@strudel/sampler):

```sh
cd samples
npx @strudel/sampler
```

Then you can load it via:

```js
samples('http://localhost:5432/');

n("<0 1 2>").s("swoop smash")
```

The handy thing about `@strudel/sampler` is that it auto-generates the `strudel.json` file based on your folder structure.
You can see what it generated by going to `http://localhost:5432` with your browser.

Note: You need NodeJS (https://nodejs.org/) installed on your system for this to work.

#### Specifying Pitch

To make sure your samples are in tune when playing them with `note`, you can specify a base pitch like this:

```js
samples({
  'gtr': 'gtr/0001_cleanC.wav',
  'moog': { 'g3': 'moog/005_Mighty%20Moog%20G3.wav' },
}, 'github:tidalcycles/dirt-samples');
note("g3 [bb3 c4] <g4 f4 eb4 f3>@2").s("gtr,moog").clip(1)
  .gain(.5)
```

We can also declare different samples for different regions of the keyboard:

```js
setcpm(60)
samples({
  'moog': {
    'g2': 'moog/004_Mighty%20Moog%20G2.wav',
    'g3': 'moog/005_Mighty%20Moog%20G3.wav',
    'g4': 'moog/006_Mighty%20Moog%20G4.wav',
  }}, 'github:tidalcycles/dirt-samples')

note("g2!2 <bb2 c3>!2, <c4@3 [<eb4 bb3> g4 f4]>")
.s('moog').clip(1)
.gain(.5)
```

The sampler will always pick the closest matching sample for the current note!

Note that this notation for pitched sounds also works inside a `strudel.json` file.

#### Shabda

If you don't want to select samples by hand, there is also the wonderful tool called shabda (https://shabda.ndre.gr/).
With it, you can enter any sample name(s) to query from freesound.org (https://freesound.org/). Example:

```js
samples('shabda:bass:4,hihat:4,rimshot:2')

$: n("0 1 2 3 0 1 2 3").s('bass')
$: n("0 1*2 2 3*2").s('hihat').clip(1)
$: n("~ 0 ~ 1 ~ 0 0 1").s('rimshot')
```

You can also generate artificial voice samples with any text, in multiple languages.
Note that the language code and the gender parameters are optional and default to `en-GB` and `f`

```js
samples('shabda/speech:the_drum,forever')
samples('shabda/speech/fr-FR/m:magnifique')

$: s("the_drum*2").chop(16).speed(rand.range(0.85,1.1))
$: s("forever magnifique").slow(4).late(0.125)
```

### Sampler Effects

Sampler effects are functions that can be used to change the behaviour of sample playback.

#### begin

`begin(amount: Number|Pattern)`

A pattern of numbers from 0 to 1. Skips the beginning of each sample, e.g. `0.25` to cut off the first quarter from each sample.

**Parameters**

- `amount` (`Number|Pattern`): between 0 and 1, where 1 is the length of the sample

**Examples**

```js
samples({ rave: 'rave/AREUREADY.wav' }, 'github:tidalcycles/dirt-samples')
s("rave").begin("<0 .25 .5 .75>").fast(2)
```

#### end

`end(length: Number|Pattern)`

Pattern method — call it on a pattern: `pattern.end(...)`.

The same as .begin, but cuts off the end off each sample.

**Parameters**

- `length` (`Number|Pattern`): 1 = whole sample,.5 = half sample,.25 = quarter sample etc..

**Examples**

```js
s("bd*2,oh*4").end("<.1 .2 .5 1>").fast(2)
```

#### loop

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

#### loopBegin

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

#### loopEnd

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

#### cut

`cut(group: Number|Pattern)`

In the style of classic drum-machines, `cut` will stop a playing sample as soon as another samples with in same cutgroup is to be played. An example would be an open hi-hat followed by a closed one, essentially muting the open.

**Parameters**

- `group` (`Number|Pattern`): cut group number

**Examples**

```js
s("[oh hh]*4").cut(1)
```

#### clip

`clip(factor: Number|Pattern)`

Synonyms: legato

Multiplies the duration with the given number. Also cuts samples off at the end if they exceed the duration.

**Parameters**

- `factor` (`Number|Pattern`): = 0

**Examples**

```js
note("c a f e").s("piano").clip("<.5 1 2>")
```

#### loopAt

`loopAt() -> Pattern`

Pattern method — call it on a pattern: `pattern.loopAt(...)`.

Makes the sample fit the given number of cycles by changing the speed.

**Examples**

```js
samples({ rhodes: 'https://cdn.freesound.org/previews/132/132051_316502-lq.mp3' })
s("rhodes").loopAt(2)
```

#### fit

`fit()`

Makes the sample fit its event duration. Good for rhythmical loops like drum breaks.
Similar to `loopAt`.

**Examples**

```js
samples({ rhodes: 'https://cdn.freesound.org/previews/132/132051_316502-lq.mp3' })
s("rhodes/2").fit()
```

#### chop

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

#### striate

`striate() -> Pattern`

Pattern method — call it on a pattern: `pattern.striate(...)`.

Cuts each sample into the given number of parts, triggering progressive portions of each sample at each loop.

**Examples**

```js
s("numbers:0 numbers:1 numbers:2").striate(6).slow(3)
```

#### slice

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

#### splice

`splice()`

Works the same as slice, but changes the playback speed of each slice to match the duration of its step.

**Examples**

```js
samples('github:tidalcycles/dirt-samples')
s("breaks165")
.splice(8,  "0 1 [2 3 0]@2 3 0@2 7")
```

#### scrub

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

#### speed

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

After samples, let's see what Synths afford us.

## Synths

In addition to the sampling engine, strudel comes with a synthesizer to create sounds on the fly.

### Basic Waveforms

The basic waveforms are `sine`, `sawtooth`, `square` and `triangle`, which can be selected via `sound` (or `s`):

```js
note("c2 <eb2 <g2 g1>>".fast(2))
.sound("<sawtooth square triangle sine>")
._scope()
```

If you don't set a `sound` but a `note` the default value for `sound` is `triangle`!

### Noise

You can also use noise as a source by setting the waveform to: `white`, `pink` or `brown`. These are different
flavours of noise, here written from hard to soft.

```js
sound("<white pink brown>")._scope()
```

Here's a more musical example of how to use noise for hihats:

```js
sound("bd*2,<white pink brown>*8")
.decay(.04).sustain(0)._scope()
```

Some amount of pink noise can also be added to any oscillator by using the `noise` paremeter:

```js
note("c3").noise("<0.1 0.25 0.5>")._scope()
```

You can also use the `crackle` type to play some subtle noise crackles. You can control noise amount by using the `density` parameter:

```js
s("crackle*4").density("<0.01 0.04 0.2 0.5>".slow(2))._scope()
```

#### Additive Synthesis

Periodic waveforms are composed of several harmonics (https://en.wikipedia.org/wiki/Harmonic) above a fundamental frequency, lying at integer multiples. These overtones combine to give a sound its unique timbral quality.

For the basic waveforms, we offer you control over these harmonics with the `partials` and `phases` functions.

##### Partials

`partials` refers to the magnitude of each harmonic relative to the fundamental frequency. They can thus be used to spectrally filter these waveforms and tame some of their harshness:

```js
note("c2 <eb2 <g2 g1>>".fast(2))
.sound("sawtooth")
.partials([1, 1, "<1 0>", "<1 0>", "<1 0>", "<1 0>", "<1 0>"])
._scope()
```

`partials` can also be used to construct _new_ waveforms not present in our basic set with the 'user' sound source:

```js
note("c2 <eb2 <g2 g1>>".fast(2))
.sound("user")
.partials([1, 0, 0.3, 0, 0.1, 0, 0, 0.3])
._scope()
```

We may algorithmically construct lists of magnitudes with Javascript code like:

```js
const numHarmonics = 22;
note("c2 <eb2 <g2 g1>>".fast(2))
.sound("saw")
.partials(new Array(numHarmonics).fill(1))
._scope()
```

which acts as a spectral filter. Or:

```js
note("c2 <eb2 <g2 g1>>").fast(2)
.sound("user")
.partials(new Array(50).fill(0)
  .map((_, idx) => ((-1) ** (idx + 1)) / (idx + 1))
)
._scope()
```

which recovers a familiar waveform.

`partials` is also compatible with pattern functions designed to produce lists, like `randL` or `binaryL`:

```js
note("c2 <eb2 <g2 g1>>").fast(2)
.sound("user")
.partials(randL(10))
._scope()
```

and with lists _of_ patterns:

```js
note("c2 <eb2 <g2 g1>>".fast(4))
.sound("user")
.partials([1, 0, "0 1", "0 1 0.3", rand])
._scope()
```

Note that the first value in the `partials` array controls the magnitude of the fundamental harmonic rather than the DC offset, which is fixed at 0.

##### Phases

Earlier, we mentioned that periodic waveforms can be broken into a set of harmonics above a fundamental frequency. Each harmonic has two defining properties: its magnitude (how loud it is) and its phase, which determines where in its cycle that sine wave starts when the waveform is built.

These phases too can be declared in Strudel and can give your sounds interesting depth.

```js
s("saw").seg(16).n(irand(12)).scale("F1:minor")
  .penv(48).panchor(0).pdec(0.05)
  .delay(0.25).room(0.25)
  .compressor(-20).vib(0.3)
  .partials(randL(200))
  .phases(randL(200))
```

### Vibrato

#### vib

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

#### vibmod

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

### FM Synthesis

FM Synthesis is a technique that changes the frequency of a basic waveform rapidly to alter the timbre.

You can use fm with any of the above waveforms, although the below examples all use the default triangle wave.

#### fmi

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

#### fmh

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

#### fmattack

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

#### fmdecay

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

#### fmsustain

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

#### fmenv

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

### Wavetable Synthesis

Strudel can also use the sampler to load custom waveforms as a replacement of the default waveforms used by WebAudio for the base synth. A default set of more than 1000 wavetables is accessible by default (coming from the AKWF (https://www.adventurekid.se/akrt/waveforms/adventure-kid-waveforms/) set). You can also import/use your own. A wavetable is a one-cycle waveform, which is then repeated to create a sound at the desired frequency. It is a classic but very effective synthesis technique.

Any sample preceded by the `wt_` prefix will be loaded as a wavetable. This means that the `loop` argument will be set to `1` by default. You can scan over the wavetable by using `loopBegin` and `loopEnd` as well.

```js
samples('bubo:waveforms');
note("<[g3,b3,e4]!2 [a3,c3,e4] [b3,d3,f#4]>")
.n("<1 2 3 4 5 6 7 8 9 10>/2").room(0.5).size(0.9)
.s('wt_flute').velocity(0.25).often(n => n.ply(2))
.release(0.125).decay("<0.1 0.25 0.3 0.4>").sustain(0)
.cutoff(2000).cutoff("<1000 2000 4000>").fast(4)
._scope()
```

### ZZFX

The "Zuper Zmall Zound Zynth" ZZFX (https://github.com/KilledByAPixel/ZzFX) is also integrated in strudel.
Developed by Frank Force (https://frankforce.com/), it is a synth and FX engine originally intended to be used for size coding games.

It has 20 parameters in total, here is a snippet that uses all:

```js
note("c2 eb2 f2 g2") // also supports freq
  .s("{z_sawtooth z_tan z_noise z_sine z_square}%4")
  .zrand(0) // randomization
  // zzfx envelope
  .attack(0.001)
  .decay(0.1)
  .sustain(.8)
  .release(.1)
  // special zzfx params
  .curve(1) // waveshape 1-3
  .slide(0) // +/- pitch slide
  .deltaSlide(0) // +/- pitch slide (?)
  .noise(0) // make it dirty
  .zmod(0) // fm speed
  .zcrush(0) // bit crush 0 - 1
  .zdelay(0) // simple delay
  .pitchJump(0) // +/- pitch change after pitchJumpTime
  .pitchJumpTime(0) // >0 time after pitchJump is applied
  .lfo(0) // >0 resets slide + pitchJump + sets tremolo speed
  .tremolo(0.5) // 0-1 lfo volume modulation amount
  //.duration(.2) // overwrite strudel event duration
  //.gain(1) // change volume
  ._scope() // vizualise waveform (not zzfx related)
```

Note that you can also combine zzfx with all the other audio fx (next chapter).

Next up: Audio Effects...

## Audio Effects

Whether you're using a synth or a sample, you can apply any of the following built-in audio effects.
As you might suspect, the effects can be chained together, and they accept a pattern string as their argument.

### Signal chain

[diagram: signal chain]

The signal chain in Strudel is as follows:

- An sound-generating event is triggered by a pattern
  - This has a start time and a duration, which is usually
    controlled by the note length and ADSR parameters
  - If we exceed the max polyphony, old sounds begin to die off
  - Muted sounds (one whose `s` value is `-`, `~`, or `_`) are skipped
- A sound is produced (through, say, a sample or an oscillator)
  - This is where detune-based effects (like `detune`, `penv`, etc. occur)
- The following will occur _in order_ and only if they've been called in the pattern. Note that all of these are
  single use effects, meaning that multiple occurrences of them in a pattern will simply override the values
  (e.g. you can't do `s("bd").lpf(100).distort(2).lpf(800)` to lowpass, distort, and then lowpass
  again)
  - Phase vocoder (`stretch`)
  - Gain is applied (`gain`)
    - This is where the main (volume) ADSR happens
  - A lowpass filter (`lpf`)
  - A highpass filter (`hpf`)
  - A bandpass filter (`bandpass`)
  - A vowel filter (`vowel`)
  - Sample rate reduction (`coarse`)
  - Bit crushing (`crush`)
  - Waveshape distortion (`shape`)
  - Normal distortion (`distort`)
  - Tremolo (`tremolo`)
  - Compressor (`compressor`)
  - Panning (`pan`)
  - Phaser (`phaser`)
  - Postgain (`post`)
- The sound is then split into multiple destinations
  - Dry output (amount controlled by `dry` parameter)
  - The sends
    - Analyzers
      - These are used for tooling like `scope` and `spectrum` and their setup usually happens behind the scenes
    - Delay (amount controlled by `delay` parameter)
    - Reverb (amount controlled by `room` parameter)
- The dry output, delay, and reverb are joined into what is called the "orbit" of the pattern (see more in the section below)
  - The `duck` effect affects the volume of all signals in the orbit
  - The orbit is then sent to the mixer

#### Orbits

Orbits are the way in which outputs are handled in Strudel. They also prescribe which delay and reverb to associate with the dry signal.
By default, all orbits are mixed down to channels `1` and `2` in stereo, however with the "Multi Channel Orbits" setting
(under Settings at the right) you can use them as individual 2 channel stereo outs (orbit `i` will be mapped to
to channels `2i` and `2i + 1`). You can then use routers like Blackhole 16 to retrieve and record all of the channels in a DAW for later processing.

The default orbit is `1` and it is set with `orbit`. You may send a sound to multiple orbits via mininotation

```js
s("white").orbit("2,3,4").gain(0.2)
```

but please be careful as this will create three copies of the sound behind the scenes, meaning that if they are mixed
down to a single output, they will triple the volume. We've reduced the gain here to save your ears.

⚠️ There is only one delay and reverb per orbit, so please be aware that if you attempt to change the parameters on two
patterns pointing to the same orbit, it can lead to unpredictable results. Compare, for example, this pretty pluck
with a large reverb:

```js
$: s("triangle*4").decay(0.5).n(irand(12)).scale('C minor')
  .room(1).roomsize(10)
```

versus the same pluck with a muted kick drum coming in and overwriting the `roomsize` value:

```js
$: s("triangle*4").decay(0.5).n(irand(12)).scale('C minor')
  .room(1).roomsize(10)

$: s("bd*4").room(0.01).roomsize(0.01).postgain(0)
```

This is due to them sharing the same orbit: the default of `1`. It can be corrected simply by updating the orbits to be
distinct:

```js
$: s("triangle*4").decay(0.5).n(irand(12)).scale('C minor')
  .room(1).roomsize(10).orbit(2)

$: s("bd*4").room(0.01).roomsize(0.01).postgain(0)
```

#### Continuous changes

As all of the above is triggered by a _sound occurring_, it is often the case that parameters may not be
modified continuously in time. For example,

```js
s("supersaw").lpf(tri.range(100, 5000).slow(2))
```

Will not produce a continually LFO'd low-pass filter due to the `tri` only being sampled every time the note hits
(in this case the default of once per cycle). You can fake it by introducing more sound-generating events, e.g.:

```js
s("supersaw").seg(16).lpf(tri.range(100, 5000).slow(2))
```

Some parameters _do_ induce continuous variations in time, though:

- The ADSR curve (governed by `attack`, `sustain`, `decay`, `release`)
- The pitch envelope curve (governed by `penv` and its associated ADSR)
- The FM curve (`fmenv`)
- The filter envelopes (`lpenv`, `hpenv`, `bpenv`)
- Tremolo (`tremolo`)
- Phaser (`phaser`)
- Vibrato (`vib`)
- Ducking (`duckorbit`)

### Filters

Filters are an essential building block of subtractive synthesis (https://en.wikipedia.org/wiki/Subtractive_synthesis).
Strudel comes with 3 types of filters:

- low-pass filter: low frequencies may _pass_, high frequencies are cut off
- high-pass filter: high frequencies may _pass_, low frequencies are cut off
- band-pass filters: only a frequency band may _pass_, low and high frequencies around are cut off

Each filter has 2 parameters:

- cutoff: the frequency at which the filter starts to work. e.g. a low-pass filter with a cutoff of 1000Hz allows frequencies below 1000Hz to pass.
- q-value: Controls the resonance of the filter. Higher values sound more aggressive. Also see Q-Factor (https://en.wikipedia.org/wiki/Q_factor)

#### lpf

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

#### lpq

`lpq(q: Number|Pattern)`

Synonyms: resonance

Controls the **l**ow-**p**ass **q**-value.

**Parameters**

- `q` (`Number|Pattern`): resonance factor between 0 and 50

**Examples**

```js
s("bd sd [~ bd] sd,hh*8").lpf(2000).lpq("<0 10 20 30>")
```

#### hpf

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

#### hpq

`hpq(q: Number|Pattern)`

Synonyms: hresonance

Controls the **h**igh-**p**ass **q**-value.

**Parameters**

- `q` (`Number|Pattern`): resonance factor between 0 and 50

**Examples**

```js
s("bd sd [~ bd] sd,hh*8").hpf(2000).hpq("<0 10 20 30>")
```

#### bpf

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

#### bpq

`bpq(q: Number|Pattern)`

Synonyms: bandq

Sets the **b**and-**p**ass **q**-factor (resonance).

**Parameters**

- `q` (`Number|Pattern`): q factor

**Examples**

```js
s("bd sd [~ bd] sd").bpf(500).bpq("<0 1 2 3>")
```

#### ftype

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

#### vowel

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

### Amplitude Modulation

Amplitude modulation changes the amplitude (gain) periodically over time.

#### am

`am` is documented on the website but has no doc.json entry; see API-REFERENCE.md.

#### tremolosync

`tremolosync(cycles: Number|Pattern)`

Synonyms: tremsync

Modulate the amplitude of a sound with a continuous waveform

**Parameters**

- `cycles` (`Number|Pattern`): modulation speed in cycles

**Examples**

```js
note("d d d# d".fast(4)).s("supersaw").tremolosync("4").tremoloskew("<1 .5 0>")
```

#### tremolodepth

`tremolodepth(depth: Number|Pattern)`

Synonyms: tremdepth

Depth of amplitude modulation

**Parameters**

- `depth` (`Number|Pattern`)

**Examples**

```js
note("a1 a1 a#1 a1".fast(4)).s("pulse").tremsync(4).tremolodepth("<1 2 .7>")
```

#### tremoloskew

`tremoloskew(amount: Number|Pattern)`

Synonyms: tremskew

Alter the shape of the modulation waveform

**Parameters**

- `amount` (`Number|Pattern`): between 0 & 1, the shape of the waveform

**Examples**

```js
note("{f a c e}%16").s("sawtooth").tremsync(4).tremoloskew("<.5 0 1>")
```

#### tremolophase

`tremolophase(offset: Number|Pattern)`

Synonyms: tremphase

Alter the phase of the modulation waveform

**Parameters**

- `offset` (`Number|Pattern`): the offset in cycles of the modulation

**Examples**

```js
note("{f a c e}%16").s("sawtooth").tremsync(4).tremolophase("<0 .25 .66>")
```

#### tremoloshape

`tremoloshape(shape: Number|Pattern)`

Synonyms: tremshape

Shape of amplitude modulation

**Parameters**

- `shape` (`Number|Pattern`): tri | square | sine | saw | ramp

**Examples**

```js
note("{f g c d}%16").tremsync(4).tremoloshape("<sine tri square>").s("sawtooth")
```

### Amplitude Envelope

The amplitude envelope (https://en.wikipedia.org/wiki/Envelope_(music)) controls the dynamic contour of a sound.
Strudel uses ADSR envelopes, which are probably the most common way to describe an envelope:

ADSR (https://upload.wikimedia.org/wikipedia/commons/thumb/e/ea/ADSR_parameter.svg/1920px-ADSR_parameter.svg.png)

image link (https://commons.wikimedia.org/wiki/File:ADSR_parameter.svg)

#### attack

`attack(attack: Number|Pattern)`

Synonyms: att

Amplitude envelope attack time: Specifies how long it takes for the sound to reach its peak value, relative to the onset.

**Parameters**

- `attack` (`Number|Pattern`): time in seconds.

**Examples**

```js
note("c3 e3 f3 g3").attack("<0 .1 .5>")
```

#### decay

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

#### sustain

`sustain(gain: Number|Pattern)`

Synonyms: sus

Amplitude envelope sustain level: The level which is reached after attack / decay, being sustained until the offset.

**Parameters**

- `gain` (`Number|Pattern`): sustain level between 0 and 1

**Examples**

```js
note("c3 e3 f3 g3").decay(.2).sustain("<0 .1 .4 .6 1>")
```

#### release

`release(time: Number|Pattern)`

Synonyms: rel

Amplitude envelope release time: The time it takes after the offset to go from sustain level to zero.

**Parameters**

- `time` (`Number|Pattern`): release time in seconds

**Examples**

```js
note("c3 e3 g3 c4").release("<0 .1 .4 .6 1>/2")
```

#### adsr

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

### Filter Envelope

Each filter can receive an additional filter envelope controlling the cutoff value dynamically. It uses an ADSR envelope similar to the one used for amplitude. There is an additional parameter to control the depth of the filter modulation: `lpenv`|`hpenv`|`bpenv`. This allows you to play subtle or huge filter modulations just the same by only increasing or decreasing the depth.

```js
note("[c eb g <f bb>](3,8,<0 1>)".sub(12))
  .s("<sawtooth>/64")
  .lpf(sine.range(300,2000).slow(16))
  .lpa(0.005)
  .lpd(perlin.range(.02,.2))
  .lps(perlin.range(0,.5).slow(3))
  .lpq(sine.range(2,10).slow(32))
  .release(.5)
  .lpenv(perlin.range(1,8).slow(2))
  .ftype('24db')
  .room(1)
  .juxBy(.5,rev)
  .sometimes(add(note(12)))
  .stack(s("bd*2").bank('RolandTR909'))
  .gain(.5).fast(2)
```

There is one filter envelope for each filter type and thus one set of envelope filter parameters preceded either by `lp`, `hp` or `bp`:

- `lpattack`, `lpdecay`, `lpsustain`, `lprelease`, `lpenv`: filter envelope for the lowpass filter.
  - alternatively: `lpa`, `lpd`, `lps`, `lpr` and `lpe`.
- `hpattack`, `hpdecay`, `hpsustain`, `hprelease`, `hpenv`: filter envelope for the highpass filter.
  - alternatively: `hpa`, `hpd`, `hps`, `hpr` and `hpe`.
- `bpattack`, `bpdecay`, `bpsustain`, `bprelease`, `bpenv`: filter envelope for the bandpass filter.
  - alternatively: `bpa`, `bpd`, `bps`, `bpr` and `bpe`.

#### lpattack

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

#### lpdecay

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

#### lpsustain

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

#### lprelease

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

#### lpenv

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

### Pitch Envelope

You can also control the pitch with envelopes!
Pitch envelopes can breathe life into static sounds:

```js
n("<-4,0 5 2 1>*<2!3 4>")
  .scale("<C F>/8:pentatonic")
  .s("gm_electric_guitar_jazz")
  .penv("<.5 0 7 -2>*2").vib("4:.1")
  .phaser(2).delay(.25).room(.3)
  .size(4).fast(1.5)
```

You also create some lovely chiptune-style sounds:

```js
n(run("<4 8>/16")).jux(rev)
.chord("<C^7 <Db^7 Fm7>>")
.dict('ireal')
.voicing().add(note("<0 1>/8"))
.dec(.1).room(.2)
.segment("<4 [2 8]>")
.penv("<0 <2 -2>>").patt(.02).fast(2)
```

Let's break down all pitch envelope controls:

#### pattack

`pattack(time: Number|Pattern)`

Synonyms: patt

Attack time of pitch envelope.

**Parameters**

- `time` (`Number|Pattern`): time in seconds

**Examples**

```js
note("c eb g bb").pattack("0 .1 .25 .5").slow(2)
```

#### pdecay

`pdecay(time: Number|Pattern)`

Synonyms: pdec

Decay time of pitch envelope.

**Parameters**

- `time` (`Number|Pattern`): time in seconds

**Examples**

```js
note("<c eb g bb>").pdecay("<0 .1 .25 .5>")
```

#### prelease

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

#### penv

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

#### pcurve

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

#### panchor

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

### Dynamics

#### gain

`gain(amount: Number|Pattern)`

Controls the gain by an exponential amount.

**Parameters**

- `amount` (`Number|Pattern`): gain.

**Examples**

```js
s("hh*8").gain(".4!2 1 .4!2 1 .4 1").fast(2)
```

#### velocity

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

#### compressor

`compressor(...)`

Requires arguments: at least 2.

Dynamics Compressor. The params are `compressor("threshold:ratio:knee:attack:release")`
More info here (https://developer.mozilla.org/en-US/docs/Web/API/DynamicsCompressorNode?retiredLocale=de#instance_properties)

**Examples**

```js
s("bd sd [~ bd] sd,hh*8")
.compressor("-20:20:10:.002:.02")
```

#### postgain

`postgain(...)`

Requires arguments: at least 2.

Gain applied after all effects have been processed.

**Examples**

```js
s("bd sd [~ bd] sd,hh*8")
.compressor("-20:20:10:.002:.02").postgain(1.5)
```

#### xfade

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

### Panning

#### jux

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

#### juxBy

`juxBy()`

Synonyms: juxby

Jux with adjustable stereo width. 0 = mono, 1 = full stereo.

**Examples**

```js
s("bd lt [~ ht] mt cp ~ bd hh").juxBy("<0 .5 1>/2", rev)
```

#### pan

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

### Waveshaping

#### coarse

`coarse(factor: Number|Pattern)`

Fake-resampling for lowering the sample rate. Caution: This effect seems to only work in chromium based browsers

**Parameters**

- `factor` (`Number|Pattern`): 1 for original 2 for half, 3 for a third and so on.

**Examples**

```js
s("bd sd [~ bd] sd,hh*8").coarse("<1 4 8 16 32>")
```

#### crush

`crush(depth: Number|Pattern)`

Bit crusher effect.

**Parameters**

- `depth` (`Number|Pattern`): between 1 (for drastic reduction in bit-depth) to 16 (for barely no reduction).

**Examples**

```js
s("<bd sd>,hh*3").fast(2).crush("<16 8 7 6 5 4 3 2>")
```

#### distort

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

### Global Effects

#### Local vs Global Effects

While the above listed "local" effects will always create a separate effects chain for each event,
global effects use the same chain for all events of the same orbit:

#### orbit

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

#### Delay

##### delay

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

##### delaytime

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

##### delayfeedback

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

#### Reverb

##### room

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

##### roomsize

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

##### roomfade

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

##### roomlp

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

##### roomdim

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

##### iresponse

`iresponse(sample: String|Pattern)`

Synonyms: ir

Sets the sample to use as an impulse response for the reverb.

**Parameters**

- `sample` (`String|Pattern`): to use as an impulse response

**Examples**

```js
s("bd sd [~ bd] sd").room(.8).ir("<shaker_large:0 shaker_large:2>")
```

#### Phaser

##### phaser

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

##### phaserdepth

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

##### phasercenter

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

##### phasersweep

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

#### Duck

##### duckorbit

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

##### duckattack

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

##### duckdepth

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

Next, we'll look at input / output via MIDI, OSC and other methods.

## MIDI, OSC and MQTT

Normally, Strudel is used to pattern sound, using its own 'web audio (https://developer.mozilla.org/en-US/docs/Web/API/Web_Audio_API)'-based synthesiser called SuperDough (https://codeberg.org/uzu/strudel/src/branch/main/packages/superdough).

It is also possible to pattern other things with Strudel, such as software and hardware synthesisers with MIDI, other software using Open Sound Control/OSC (including the SuperDirt (https://github.com/musikinformatik/SuperDirt/) synthesiser commonly used with Strudel's sibling TidalCycles (https://tidalcycles.org/)), or the MQTT 'internet of things' protocol.

### MIDI

Strudel supports MIDI without any additional software (thanks to webmidi (https://npmjs.com/package/webmidi)), just by adding methods to your pattern:

#### midin

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

#### midikeys

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

#### midi(outputName?,options?)

Either connect a midi device or use the IAC Driver (Mac) or Midi Through Port (Linux) for internal midi messages.
If no outputName is given, it uses the first midi output it finds.

```js
$: chord("<C^7 A7 Dm7 G7>").voicing().midi('IAC Driver')
```

In the console, you will see a log of the available MIDI devices as soon as you run the code,
e.g.

```txt
 `Midi connected! Using "Midi Through Port-0".`
```

The `.midi()` function accepts an options object with the following properties:

```js
$: note("d e c a f").midi('IAC Driver', { isController: true, midimap: 'default'})
```

**Available Options**

| Option       | Type          | Default   | Description                                                            |
| ------------ | ------------- | --------- | ---------------------------------------------------------------------- |
| isController | boolean       | false     | When true, disables sending note messages. Useful for MIDI controllers |
| latencyMs    | number        | 34        | Latency in milliseconds to align MIDI with audio engine                |
| noteOffsetMs | number        | 10        | Offset in milliseconds for note-off messages to prevent glitching      |
| midichannel  | number        | 1         | Default MIDI channel (1-16)                                            |
| velocity     | number        | 0.9       | Default note velocity (0-1)                                            |
| gain         | number        | 1         | Default gain multiplier for velocity (0-1)                             |
| midimap      | string        | 'default' | Name of MIDI mapping to use for control changes                        |
| midiport     | string/number | -         | MIDI device name or index                                              |

##### midiport

Selects the MIDI output device to use, pattern can be used to switch between devices.

```js
$: midiport('IAC Driver');
$: note('c a f e').midiport('<0 1 2 3>').midi();
```

`midiport(port: Number|Pattern)`

MIDI port: Sets the MIDI port for the event.

**Parameters**

- `port` (`Number|Pattern`): MIDI port

**Examples**

```js
note("c a f e").midiport("<0 1 2 3>").midi()
```

#### midichan(number)

Selects the MIDI channel to use. If not used, `.midi` will use channel 1 by default.

#### midicmd(command)

`midicmd` sends MIDI system real-time messages to control timing and transport on MIDI devices.

It supports the following commands:

- `clock`/`midiClock` - Sends MIDI timing clock messages
- `start` - Sends MIDI start message
- `stop` - Sends MIDI stop message
- `continue` - Sends MIDI continue message

// You can control the clock with a pattern and ensure it starts in sync when the repl begins.
// Note: It might act unexpectedly if MIDI isn't set up initially.

```js
$:stack(
  midicmd("clock*48,<start stop>/2").midi('IAC Driver')
)
```

#### control, ccn && ccv

- `control` sends MIDI control change messages to your MIDI device.
- `ccn` sets the cc number. Depends on your synths midi mapping
- `ccv` sets the cc value. normalized from 0 to 1.

```js
note("c a f e").control([74, sine.slow(4)]).midi()
```

```js
note("c a f e").ccn(74).ccv(sine.slow(4)).midi()
```

In the above snippet, `ccn` is set to 74, which is the filter cutoff for many synths. `ccv` is controlled by a saw pattern.
Having everything in one pattern, the `ccv` pattern will be aligned to the note pattern, because the structure comes from the left by default.
But you can also control cc messages separately like this:

```js
$: note("c a f e").midi()
$: ccv(sine.segment(16).slow(4)).ccn(74).midi()
```

Instead of setting `ccn` and `ccv` directly, you can also create mappings with `midimaps`:

#### midimaps

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

#### defaultmidimap

`defaultmidimap()`

configures the default midimap, which is used when no "midimap" port is set

**Examples**

```js
defaultmidimap({ lpf: 74 })
$: note("c a f e").midi();
$: lpf(sine.slow(4).segment(16)).midi();
```

#### progNum (Program Change)

`progNum` sends MIDI program change messages to switch between different presets/patches on your MIDI device.
Program change values should be numbers between 0 and 127.

```js
// Switch between programs 0 and 1 every cycle
progNum("<0 1>").midi()

// Play notes while changing programs
note("c3 e3 g3").progNum("<0 1 2>").midi()
```

Program change messages are useful for switching between different instrument sounds or presets during a performance.
The exact sound that each program number maps to depends on your MIDI device's configuration.

#### sysex, sysexid && sysexdata (System Exclusive Message)

`sysex` sends MIDI System Exclusive (SysEx) messages to your MIDI device.
ysEx messages are device-specific commands that allow deeper control over synthesizer parameters.
The value should be an array of numbers between 0-255 representing the SysEx data bytes.

```js
// Send a simple SysEx message
let id = 0x43; //Yamaha
//let id = "0x00:0x20:0x32"; //Behringer ID can be an array of numbers
let data = "0x79:0x09:0x11:0x0A:0x00:0x00"; // Set NSX-39 voice to say "Aa"
$: note("c a f e").sysex(id, data).midi();
$: note("c a f e").sysexid(id).sysexdata(data).midi();
```

The exact format of SysEx messages depends on your MIDI device's specification.
Consult your device's MIDI implementation guide for details on supported SysEx messages.

#### midibend && miditouch

`midibend` sets MIDI pitch bend (-1 - 1)
`miditouch` sets MIDI key after touch (0-1)

```js
note("c a f e").midibend(sine.slow(4).range(-0.4,0.4)).midi()
```

```js
note("c a f e").miditouch(sine.slow(4).range(0,1)).midi()
```

### OSC/SuperDirt/StrudelDirt

In TidalCycles, sound is usually generated using SuperDirt (https://github.com/musikinformatik/SuperDirt/), which runs inside SuperCollider. Strudel also supports using SuperDirt, although it requires installing some additional software.

There is also StrudelDirt (https://github.com/daslyfe/StrudelDirt) which is SuperDirt with some optimisations for working with Strudel. (A longer term aim is to merge these optimisations back into mainline SuperDirt)

#### Prequisites

To get SuperDirt to work with Strudel, you need to

1. install SuperCollider + sc3 plugins, see Tidal Docs (https://tidalcycles.org/docs/) (Install Tidal) for more info.
2. install SuperDirt, or the StrudelDirt (https://github.com/daslyfe/StrudelDirt) fork which is optimised for use with Strudel
3. install node.js (https://nodejs.org/en/)
4. download Strudel Repo (https://codeberg.org/uzu/strudel/) (or git clone, if you have git installed)
5. run `pnpm i` in the strudel directory
6. run `pnpm run osc` to start the osc server, which forwards OSC messages from Strudel REPL to SuperCollider

Now you're all set!

#### Usage (OSC/SuperDirt)

1. Start SuperCollider, either using SuperCollider IDE or by running `sclang` in a terminal
2. Open the Strudel REPL (https://strudel.cc/#cygiYmQgc2QiKS5vc2MoKQ%3D%3D)

...or test it here:

```js
s("bd sd").osc()
```

If you now hear sound, congratulations! If not, you can get help on the #strudel channel in the TidalCycles discord (https://discord.com/invite/HGEdXmRkzT).

Note: if you have the 'Audio Engine Target' in settings set to 'OSC', you do not need to add .osc() to the end of your pattern.

##### osc

`osc() -> Pattern`

Pattern method — call it on a pattern: `pattern.osc(...)`.

Sends each hap as an OSC message, which can be picked up by SuperCollider or any other OSC-enabled software.
For more info, read MIDI & OSC in the docs (https://strudel.cc/learn/input-output/)

#### SuperDirt Params

Please refer to Tidal Docs (https://tidalcycles.org/) for more info.

But can we use Strudel offline?

### MQTT

MQTT is a lightweight network protocol, designed for 'internet of things' devices. For use with strudel, you will
need access to an MQTT server known as a 'broker' configured to accept secure 'websocket' connections. You could
run one yourself (e.g. by running mosquitto (https://mosquitto.org/)), although getting an SSL certificate that
your web browser will trust might be a bit tricky for those without systems administration experience.
Alternatively, you can use a public broker (https://www.hivemq.com/mqtt/public-mqtt-broker/).

Strudel does not yet support receiving messages over MQTT, only sending them.

#### Usage (MQTT)

The following example shows how to send a pattern to an MQTT broker:

```js
"hello world"
    .mqtt(undefined, // username (undefined for open/public servers)
          undefined, // password
          '/strudel-pattern', // mqtt 'topic'
          'wss://mqtt.eclipseprojects.io:443/mqtt', // MQTT server address
          'mystrudel', // MQTT client id - randomly generated if not supplied
          0 // latency / delay before sending messages (0 = no delay)
         )
```

Other software can then receive the messages. For example using the mosquitto (https://mosquitto.org/) commandline client tools:

```txt
> mosquitto_sub -h mqtt.eclipseprojects.io -p 1883 -t "/strudel-pattern"
> hello
> world
> hello
> world
> ...
```

Control patterns will be encoded as JSON, for example:

```js
sound("sax(3,8)").speed("2 3")
  .mqtt(undefined, // username (undefined for open/public servers)
        undefined, // password
        '/strudel-pattern', // mqtt 'topic'
        'wss://mqtt.eclipseprojects.io:443/mqtt', // MQTT server address
        'mystrudel', // MQTT client id - randomly generated if not supplied
        0 // latency / delay before sending messages (0 = no delay)
       )
```

Will send messages like the following:

```txt
{"s":"sax","speed":2}
{"s":"sax","speed":2}
{"s":"sax","speed":3}
{"s":"sax","speed":2}
...
```

Libraries for receiving MQTT are available for many programming languages.

## Coding Syntax

Let's take a step back and understand how the syntax in Strudel works.

Take a look at this simple example:

```js
note("c a f e").s("piano")
```

- We have a word `note` which is followed by some brackets `()` with some words/letters/numbers inside, surrounded by quotes `"c a f e"`
- Then we have a dot `.` followed by another similar piece of code `s("piano")`.
- We can also see these texts are _highlighted_ using colours: word `note` is purple, the brackets `()` are grey, and the content inside the `""` are green. (The colors could be different if you've changed the default theme)

What happens if we try to 'break' this pattern in different ways?

```js
note(c a f e).s(piano)
```

```js
note("c a f e")s("piano")
```

```js
note["c a f e"].s{"piano"}
```

Ok, none of these seem to work...

```js
s("piano").note("c a f e")
```

This one does work, but now we only hear the first note...

So what is going on here?

### Functions, arguments and chaining

So far, we've seen the following syntax:

```js
xxx("foo").yyy("bar")
```

Generally, `xxx` and `yyy` are called _functions_ (https://en.wikipedia.org/wiki/Function_(computer_programming)), while `foo` and `bar` are called function _arguments_ or _parameters_ (https://en.wikipedia.org/wiki/Parameter_(computer_programming)).
So far, we've used the functions to declare which aspect of the sound we want to control, and their arguments for the actual data.
The `yyy` function is called a _chained_ function (https://en.wikipedia.org/wiki/Method_chaining), because it is preceded with a dot (`.`).

Generally, the idea with chaining is that code such as `a("this").b("that").c("other")` allows `a`, `b` and `c` functions to happen in a specified order, without needing to write them as three separate lines of code.
You can think of this as being similar to chaining audio effects together using guitar pedals or digital audio effects.

Strudel makes heavy use of chained functions. Here is a more sophisticated example:

```js
note("a3 c#4 e4 a4")
.s("sawtooth")
.cutoff(500)
//.delay(0.5)
.room(0.5)
```

#### Write your own chained function

You can write your own chained function using `register`. Here's the above chain but registered as a reusable, chained function.

```js
const effectChain = register('effectChain', (pat) => pat
    .s("sawtooth")
    .cutoff(500)
    //.delay(0.5)
    .room(0.5)
  )
note("a3 c#4 e4 a4").effectChain()
```

Try adding `.rev()` after `effectChain()` to hear further effects added.

### Comments

The `//` in the example above is a line comment, resulting in the `delay` function being ignored.
It is a handy way to quickly turn code on and off.
Try uncommenting this line by deleting `//` and refreshing the pattern.
You can also use the keyboard shortcut `cmd-/` to toggle comments on and off.

You might noticed that some comments in the REPL samples include some words starting with a "@", like `@by` or `@license`.
Those are just a convention to define some information about the music. We will talk about it in the Music metadata section.

### Strings

Ok, so what about the content inside the quotes (e.g. `"c a f e"`)?
In JavaScript, as in most programming languages, this content is referred to as being a _string_ (https://en.wikipedia.org/wiki/String_(computer_science)).
A string is simply a sequence of individual characters.
In TidalCycles, double quoted strings are used to write _patterns_ using the mini-notation, and you may hear the phrase _pattern string_ from time to time.
If you want to create a regular string and not a pattern, you can use single quotes, e.g. `'C minor'` will not be parsed as Mini Notation.

The good news is, that this covers most of the JavaScript syntax needed for Strudel!

## Visual Feedback

There are several function that add visual feedback to your patterns.

### Mini Notation Highlighting

When you write mini notation with "double quotes" or `backticks`, the active parts of the mini notation will be highlighted:

```js
n("<0 2 1 3 2>*8")
.scale("<A1 D2>/4:minor:pentatonic")
.s("supersaw").lpf(300).lpenv("<4 3 2>*4")
```

You can change the color as well, even pattern it:

```js
n("<0 2 1 3 2>*8")
.scale("<A1 D2>/4:minor:pentatonic")
.s("supersaw").lpf(300).lpenv("<4 3 2>*4")
.color("cyan magenta")
```

### Global vs Inline Visuals

The following functions all come with in 2 variants.

**Without prefix**: renders the visual to the background of the page:

```js
note("c a f e").color("white").punchcard()
```

**With `_` prefix**: renders the visual inside the code. Allows for multiple visuals

```js
note("c a f e").color("white")._punchcard()
```

Here we see the 2 variants for `punchcard`. The same goes for all others below.
To improve readability the following demos will all use the inline variant.

### Punchcard / Pianoroll

These 2 functions render a pianoroll style visual.
The only difference between the 2 is that `pianoroll` will render the pattern directly,
while `punchcard` will also take the transformations into account that occur afterwards:

```js
note("c a f e").color("white")
._punchcard()
.color("cyan")
```

Here, the `color` is still visible in the visual, even if it is applied after `_punchcard`.
On the contrary, the color is not visible when using `_pianoroll`:

```js
note("c a f e").color("white")
._pianoroll()
.color("cyan")
```

`punchcard` is less resource intensive because it uses the same data as used for the mini notation highlighting.

The visual can be customized by passing options. Those options are the same for both functions.

What follows is the API doc of all the options you can pass:

#### pianoroll
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

### markcss

`markcss()`

Overrides the css of highlighted events. Make sure to use single quotes!

**Examples**

```js
note("c a f e")
.markcss('text-decoration:underline')
```

## Xenharmonic Functions (experimental)

These functions allow the use of scales other than your typical chromatic 12 based ones.

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

Here's an example of how to configure a basic hexany scale:

```js
i("0 1 2 3 4 5").tune("hexany15").mul("220").freq()
```

Try other scales like `hexany1`, `iraq`, `gumbeng`, `gunkali`, or `tranh3`

For a full list of available scales from tunejs, see http://abbernie.github.io/tune/scales.html

You can set your root to be a particular note with `getFreq`

```js
i("4 8 9 10 - - 5 7 9 11 - -").tune("tranh3")
    .mul(getFreq('c3'))
    .freq().clip(.5).room(1)
```

Some tunings become more pronounced with a longer reverb decay:

```js
i("<[5 6 8 10] - [5 7 9 12] -> -").tune("gumbeng")
  .mul(getFreq('c3'))
  .freq().clip(.8).room("3:10").rdim(10000).rfade(5)
```

Additionally, you can combo this with `fmap` so that the base note changes:

```js
i("9 11 12 10 - - -").tune("gunkali")
  .mul("<c3 c3 a3 d#3>".fmap(getFreq))
  .freq().legato("2 .7").room("1:15").rdim(8500).rlp(14000).rfade(8)
```

Combining this with various polyrhythm tricks can become very evocative:

```js
i("<[0 3 1 -] [-1 4 2 8]> ~ ~,<-4 -5>".add(4))
  .tune("iraq")
  .mul("<c3 d3 c#3>".fmap(getFreq))
  .freq().clip(.5).room(1).rfade(9)
```

Another helpful trick when exploring new tunings is to strum them.
Many have a much more enchanting sound that was chosen over many generations of musicians for being strummed.

Take the `sanza` tuning:

```js
i("4 5 6 7 8 9").tune("sanza")
  .mul(getFreq('c3'))
  .freq()
```

Notes 7 and 9 will clash quite a bit if you arp them normally. Many tunings will have this sort of sound, and it can feel distracting on its own.
See how close they are on the pitch wheel?

```js
i("[7 9]!3").tune("sanza").mul(getFreq('c3')).freq()._pitchwheel()
```

This quality is often due to how the tunings were formed with instruments that were played differently than a piano.
As such, some tunings are much better strummed, with the subtle clash of the detuned notes actually making the sound much more magical:

```js
i("[0 1 2 3 4 5 6]@0.3 -"
  .add("<2 5 8 1>"))
  .tune("sanza")
  .mul(getFreq('c3')).freq()
  .legato("3").room(1).rfade(5)
```

Note the legato and reverb effects make sure the sound of the strumming gets to wash together. Alternating the direction of the strum can make the
tones sound even more alive, too.

The `tranh3` tuning has a similar set of notes, with two clashing. You might trying plugging that in above and see if you find a favorite strumming pattern.

You can also give tune a list of frequencies to use as the scale:

```js
i("0 1 2 3 4").tune([
    261.6255653006,
    302.72962012827,
    350.29154279212,
    405.32593044476,
    469.00678383895,
    523.2511306012
  ]).mul(220).freq();
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
