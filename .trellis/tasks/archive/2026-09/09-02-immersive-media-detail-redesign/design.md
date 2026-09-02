# Design

## Visual hierarchy

1. Shorter cinematic hero with poster, identity, metadata, overview, and primary actions.
2. Adaptive playback-options dock whose populated groups share available width.
3. Full-width still rail using large 16:9 cards.
4. Full-width cast rail using portrait cards and concise role labels.
5. Compact technical metadata strip, then collections and related titles.

## Horizontal rail contract

Create `ImmersiveMediaRail.vue` as a reusable presentation/interaction component. It owns the overflow element, hidden-scrollbar CSS, edge state, resize/scroll observation, and glass arrow controls. Content remains a slot so `MediaDetailView` owns semantic image/person markup.

The component uses native overflow as the source of truth. Buttons call smooth `scrollBy`; scroll/resize events compute whether backward/forward movement remains. Controls are keyboard-focusable and do not replace touch/trackpad input. Mobile/coarse-pointer CSS suppresses arrows while preserving horizontal touch scrolling.

## Safety

No provider URLs, playback state, or DataSource contracts move into the component. Images retain lazy loading and alt text. Existing episode-rail behavior stays isolated.
