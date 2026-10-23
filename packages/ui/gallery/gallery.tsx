// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

/*
 * A page showing every component with synthetic data, for checking them in a
 * browser at desktop and mobile widths in both themes. Not part of the
 * package build. `pnpm --filter @wardlock/ui gallery` serves it; add
 * `?theme=light` or `?theme=dark` to force a theme.
 */
import { StrictMode, useState } from 'react';
import { createRoot } from 'react-dom/client';
import '../styles/index.css';
import './gallery.css';
import {
  BarList,
  BottomBar,
  BulkBar,
  Button,
  Card,
  CommandPalette,
  Dialog,
  Drawer,
  HubLayout,
  Menu,
  MenuCheckboxItem,
  MenuItem,
  PageHeader,
  Popover,
  PopoverFilter,
  Rail,
  RailButton,
  RailItem,
  SegmentedControl,
  Sparkline,
  StackedBars,
  StatusTag,
  Tab,
  TagChip,
  Tabs,
  Toast,
  Ward,
  Wire,
  type CommandGroup,
  type StackedBarsPoint,
  type TagSlot,
} from '../src/index.js';

const theme = new URLSearchParams(location.search).get('theme');
if (theme === 'light' || theme === 'dark')
  document.documentElement.dataset.theme = theme;

const labels: { name: string; slot: TagSlot }[] = [
  { name: 'neutral', slot: 0 },
  { name: 'blue', slot: 1 },
  { name: 'orange', slot: 2 },
  { name: 'aqua', slot: 3 },
  { name: 'yellow', slot: 4 },
  { name: 'magenta', slot: 5 },
  { name: 'green', slot: 6 },
  { name: 'violet', slot: 7 },
  { name: 'red', slot: 8 },
];

// A deterministic wave, so screenshots are stable.
const days: StackedBarsPoint[] = Array.from({ length: 30 }, (_, i) => {
  const date = new Date(Date.UTC(2026, 9, 1 + i));
  const label = date.toLocaleDateString('en-NZ', {
    weekday: 'short',
    day: 'numeric',
    month: 'short',
    timeZone: 'UTC',
  });
  const tick = date.toLocaleDateString('en-NZ', {
    day: 'numeric',
    month: 'short',
    timeZone: 'UTC',
  });
  const allowed = Math.round(90 + 60 * Math.sin(i / 3) + (i % 7) * 9);
  const refused = Math.round(6 + 5 * Math.cos(i / 2));
  return { key: tick, label, tick, values: [allowed, refused] };
});

const pages: CommandGroup = {
  label: 'Pages',
  items: [
    { id: 'p1', label: 'Overview', icon: '◇' },
    { id: 'p2', label: 'Analytics', icon: '◇' },
  ],
};
const places: CommandGroup = {
  label: 'Places',
  items: ['alpha', 'beta', 'gamma', 'delta', 'epsilon'].map((name) => ({
    id: name,
    label: name,
    hint: `${name}.example.test`,
    icon: '●',
  })),
};

function PickerDemo() {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [state, setState] = useState<Record<string, boolean | 'mixed'>>({
    blue: true,
    orange: 'mixed',
  });
  const shown = labels.filter((label) => label.name.includes(query));
  return (
    <Popover
      label="Labels"
      open={open}
      onOpenChange={setOpen}
      trigger={(props) => (
        <Button type="button" {...props}>
          Labels
        </Button>
      )}
    >
      <PopoverFilter
        label="Labels for 3 selected"
        value={query}
        onChange={setQuery}
        placeholder="Filter or create"
      />
      <Menu label="Labels">
        {shown.map((label) => (
          <MenuCheckboxItem
            key={label.name}
            checked={state[label.name] ?? false}
            hint={String(label.slot * 3)}
            onSelect={() =>
              setState((s) => ({ ...s, [label.name]: s[label.name] !== true }))
            }
          >
            <span
              className="wl-tag-dot"
              style={{ background: `var(--color-tag-${label.slot})` }}
              aria-hidden="true"
            />
            {label.name}
          </MenuCheckboxItem>
        ))}
        {query && !labels.some((label) => label.name === query) ? (
          <MenuItem icon="+" onSelect={() => setOpen(false)}>
            Create “{query}”
          </MenuItem>
        ) : null}
      </Menu>
    </Popover>
  );
}

// Decorative glyphs for the rail demo; the dashboard draws its own icons.
const railPlaces = (
  <>
    <RailItem href="#rail" icon="◇" label="Overview" current />
    <RailItem href="#places" icon="◎" label="Places" />
    <RailItem href="#keys" icon="◆" label="Keys" />
  </>
);

function Gallery() {
  const [range, setRange] = useState<'7' | '30' | '90'>('30');
  const [selected, setSelected] = useState(0);
  const [toast, setToast] = useState<string | null>(null);
  const [toastKey, setToastKey] = useState(0);
  const [dialog, setDialog] = useState(false);
  const [palette, setPalette] = useState(false);
  const [drawer, setDrawer] = useState(false);
  const [query, setQuery] = useState('');
  const [chips, setChips] = useState(labels.slice(1, 4));
  const match = (group: CommandGroup) => ({
    ...group,
    items: group.items.filter((item) =>
      item.label.toLowerCase().includes(query.toLowerCase()),
    ),
  });

  return (
    <main className="wl-main" style={{ paddingBottom: '12rem' }}>
      <PageHeader
        title="Components"
        description="Every component with synthetic data."
      />

      <Card title="Status tags, tag chips and the ward">
        <div className="gallery-row">
          <StatusTag tone="success">Live</StatusTag>
          <StatusTag tone="warning">Expires soon</StatusTag>
          <StatusTag tone="danger">Taken down</StatusTag>
          <StatusTag tone="neutral">Draft</StatusTag>
          <StatusTag tone="ward">
            <Ward size="sm" />
            Protected
          </StatusTag>
          <StatusTag tone="public">Public</StatusTag>
          <StatusTag tone="primary">New</StatusTag>
        </div>
        <div className="gallery-row">
          {labels.map((label) => (
            <TagChip key={label.name} label={label.name} slot={label.slot} />
          ))}
        </div>
        <div className="gallery-row">
          {chips.map((chip) => (
            <TagChip
              key={chip.name}
              label={chip.name}
              slot={chip.slot}
              onRemove={() => setChips((c) => c.filter((x) => x !== chip))}
            />
          ))}
        </div>
      </Card>

      <Card title="Hub and wires">
        <HubLayout
          hub={<b className="gallery-mono">server</b>}
          label="Sections"
        >
          <Wire ward>
            <div className="gallery-head">
              <h3>First section</h3>
              <span className="gallery-muted">3 rows</span>
            </div>
            <HubLayout label="First section rows">
              <Wire ward>
                <div className="gallery-node">
                  <b className="gallery-mono">alpha</b>
                  <StatusTag tone="ward">Protected</StatusTag>
                  <Sparkline values={[3, 5, 4, 8, 6, 9, 12]} />
                  <span className="gallery-mono">1,240</span>
                </div>
              </Wire>
              <Wire tone="public">
                <div className="gallery-node">
                  <b className="gallery-mono">beta</b>
                  <StatusTag tone="public">Public</StatusTag>
                  <Sparkline values={[9, 7, 8, 4, 5, 3, 2]} />
                  <span className="gallery-mono">310</span>
                </div>
              </Wire>
              <Wire ward>
                <div className="gallery-node">
                  <b className="gallery-mono">gamma-with-a-longer-name</b>
                  <TagChip label="aqua" slot={3} />
                </div>
              </Wire>
            </HubLayout>
          </Wire>
          <Wire>
            <div className="gallery-head">
              <h3>Second section</h3>
              <span className="gallery-muted">empty</span>
            </div>
          </Wire>
        </HubLayout>
      </Card>

      <Card title="Tabs and segmented control">
        <Tabs label="Sections">
          <Tab href="#overview" current>
            Overview
          </Tab>
          <Tab href="#links" count={4}>
            Links
          </Tab>
          <Tab href="#access">Access</Tab>
          <Tab href="#domains" count={2}>
            Domains
          </Tab>
          <Tab href="#deploys" count={18}>
            Deploys
          </Tab>
          <Tab href="#log">Access log</Tab>
          <Tab href="#settings">Settings</Tab>
        </Tabs>
        <SegmentedControl
          label="Range"
          value={range}
          onChange={setRange}
          options={[
            { value: '7', label: '7 days' },
            { value: '30', label: '30 days' },
            { value: '90', label: '90 days' },
          ]}
        />
      </Card>

      <Card title="Popover, bulk bar and toast">
        <div className="gallery-row">
          <PickerDemo />
          <Button type="button" onClick={() => setSelected(3)}>
            Select three
          </Button>
          <Button
            type="button"
            onClick={() => {
              setToast('Tagged 3 items blue');
              setToastKey((k) => k + 1);
            }}
          >
            Show toast
          </Button>
        </div>
      </Card>

      <Card title="Bar list">
        <BarList
          label="Top items"
          total={4210}
          items={[
            {
              key: 'a',
              label: 'alpha',
              value: 2140,
              href: '#a',
              hint: 'alpha.example.test',
            },
            { key: 'b', label: 'beta', value: 1220, href: '#b' },
            { key: 'c', label: 'gamma', value: 610 },
            { key: 'd', label: 'delta', value: 240 },
          ]}
        />
        <BarList
          label="By label"
          items={labels.slice(1, 5).map((label, i) => ({
            key: label.name,
            label: label.name,
            value: 900 - i * 200,
            slot: label.slot,
          }))}
        />
      </Card>

      <Card title="Stacked bars">
        <StackedBars
          label="Requests per day"
          series={['Allowed', 'Refused']}
          points={days}
          periodHeader="Day"
        />
      </Card>

      <Card title="Rail, bottom bar and drawer">
        <p className="gallery-muted">
          The rail shows from 48rem and the bottom bar below it; hover or focus
          a rail icon for its name.
        </p>
        <div className="gallery-rail">
          <Rail
            label="Rail example"
            productName="Product"
            homeHref="#rail"
            foot={
              <RailButton
                icon="⌕"
                label="Search"
                shortcut="⌘K"
                aria-keyshortcuts="Meta+K"
              />
            }
          >
            {railPlaces}
          </Rail>
        </div>
        <div className="gallery-bottom-bar">
          <BottomBar label="Bottom bar example">
            {railPlaces}
            <RailButton icon="⌕" label="Search" />
          </BottomBar>
        </div>
        <div className="gallery-row">
          <Button type="button" onClick={() => setDrawer(true)}>
            Open drawer
          </Button>
        </div>
      </Card>

      <Card title="Dialog and command palette">
        <div className="gallery-row">
          <Button type="button" onClick={() => setDialog(true)}>
            Open dialog
          </Button>
          <Button
            type="button"
            onClick={() => {
              setQuery('');
              setPalette(true);
            }}
          >
            Jump to
          </Button>
        </div>
      </Card>

      <Dialog
        open={dialog}
        title="Remove the example?"
        onClose={() => setDialog(false)}
        actions={
          <>
            <Button type="button" onClick={() => setDialog(false)}>
              Cancel
            </Button>
            <Button type="button" variant="danger">
              Remove example
            </Button>
          </>
        }
      >
        <p>This shows the dialog layout; it removes nothing.</p>
      </Dialog>

      <Drawer open={drawer} title="Filters" onClose={() => setDrawer(false)}>
        <p>A panel from the start edge. Links inside close it.</p>
        <a href="#rail">Back to the rail</a>
      </Drawer>

      <CommandPalette
        open={palette}
        onClose={() => setPalette(false)}
        label="Jump to"
        placeholder="Jump to a place or page"
        query={query}
        onQueryChange={setQuery}
        groups={[match(places), match(pages)]}
        onSelect={() => setPalette(false)}
        footer="7 searched"
      />

      <BulkBar
        label="Bulk actions"
        count={selected}
        onClear={() => setSelected(0)}
      >
        <button type="button">Labels</button>
        <button type="button">Move</button>
        <button type="button">Pin</button>
      </BulkBar>
      <Toast
        message={toast}
        toastKey={toastKey}
        onUndo={() => {}}
        onDismiss={() => setToast(null)}
      />
    </main>
  );
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <Gallery />
  </StrictMode>,
);
