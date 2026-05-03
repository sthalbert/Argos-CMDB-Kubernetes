import { useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import * as api from '../api';
import { useResource } from '../hooks';
import { isAdmin, useMe } from '../me';
import { AsyncView, Dash, KV, SectionTitle, Empty } from '../components';
import { PowerStatePill } from './VirtualMachines';
import { IdentityCard } from '../components/inventory/IdentityCard';
import { NetworkingCard } from '../components/inventory/NetworkingCard';
import { CapacityCard } from '../components/inventory/CapacityCard';
import { LabelsCard } from '../components/inventory/LabelsCard';
import { AnnotationsCard } from '../components/inventory/AnnotationsCard';
import { ApplicationsCard } from '../components/inventory/ApplicationsCard';
import { CuratedMetadataCard } from '../components/inventory/CuratedMetadataCard';
import { Breadcrumb } from '../components/lv/Breadcrumb';
import { PageHead } from '../components/lv/PageHead';
import { Pill } from '../components/lv/Pill';

// VirtualMachineDetail is the per-VM drill-down page. Card layout mirrors
// the Node detail page, with extra cards for cloud-native fields (image,
// keypair, NICs, security groups, block devices) that don't apply to
// Kubernetes nodes.

function formatTs(ts?: string | null): string {
  if (!ts) return '';
  try {
    return new Date(ts).toLocaleString();
  } catch {
    return ts;
  }
}

export default function VirtualMachineDetail() {
  const { id = '' } = useParams();
  const me = useMe();
  const [nonce, setNonce] = useState(0);
  const reload = () => setNonce((n) => n + 1);

  const vmState = useResource(() => api.getVirtualMachine(id), [id, nonce]);
  const acctState = useResource(
    async () => {
      // Cloud account lookup is admin-only — viewers / editors must see
      // the page render without it.
      if (!isAdmin(me)) return null;
      if (vmState.status !== 'ready') return null;
      try {
        return await api.getCloudAccount(vmState.data.cloud_account_id);
      } catch {
        return null;
      }
    },
    [me?.role ?? '', vmState.status === 'ready' ? vmState.data.cloud_account_id : ''],
  );

  return (
    <>
      <AsyncView state={vmState}>
        {(vm) => {
          const acct = acctState.status === 'ready' ? acctState.data : null;
          return (
            <>
              <Breadcrumb
                parts={[
                  { label: 'Virtual machines', to: '/virtual-machines', ariaLabel: 'Back to virtual machines' },
                  { label: vm.display_name || vm.name },
                ]}
              />
              <PageHead
                title={vm.display_name || vm.name}
                sub={vm.instance_type ?? undefined}
                actions={<>
                  <PowerStatePill state={vm.power_state} />
                  {vm.terminated_at && (
                    <Pill status="bad">Terminated</Pill>
                  )}
                </>}
              />

              <IdentityCard
                rows={[
                  { label: 'Name', value: <code>{vm.name}</code> },
                  { label: 'Display name', value: vm.display_name },
                  { label: 'Role', value: vm.role && <Pill>{vm.role}</Pill> },
                  {
                    label: 'Provider VM ID',
                    value: <code>{vm.provider_vm_id}</code>,
                  },
                  {
                    label: 'Cloud account',
                    value: acct ? (
                      isAdmin(me) ? (
                        <Link to={`/admin/cloud-accounts/${acct.id}`}>
                          <strong>{acct.name}</strong>{' '}
                          <span className="muted" style={{ fontSize: 'var(--fs-sm)' }}>
                            {acct.provider}/{acct.region}
                          </span>
                        </Link>
                      ) : (
                        <>
                          <strong>{acct.name}</strong>{' '}
                          <span className="muted" style={{ fontSize: 'var(--fs-sm)' }}>
                            {acct.provider}/{acct.region}
                          </span>
                        </>
                      )
                    ) : (
                      <span className="muted">{vm.cloud_account_id.slice(0, 8)}…</span>
                    ),
                  },
                ]}
              />

              <CuratedMetadataCard
                values={{
                  owner: vm.owner,
                  criticality: vm.criticality,
                  notes: vm.notes,
                  runbook_url: vm.runbook_url,
                  annotations: vm.annotations,
                }}
                extraDisplay={[
                  { label: 'Display name', value: vm.display_name },
                  {
                    label: 'Role',
                    value: vm.role ? <Pill>{vm.role}</Pill> : null,
                  },
                ]}
                extraFields={[
                  {
                    key: 'display_name',
                    label: 'Display name',
                    placeholder: 'web-prod-01',
                    initial: vm.display_name ?? '',
                  },
                  {
                    key: 'role',
                    label: 'Role',
                    placeholder: 'bastion / db / app',
                    initial: vm.role ?? '',
                  },
                ]}
                onSave={async (values, extras) => {
                  await api.updateVirtualMachine(vm.id, {
                    owner: values.owner,
                    criticality: values.criticality,
                    notes: values.notes,
                    runbook_url: values.runbook_url,
                    annotations: values.annotations,
                    display_name: extras.display_name,
                    role: extras.role,
                  });
                }}
                onSaved={reload}
              />

              <NetworkingCard
                rows={[
                  { label: 'Private IP', value: vm.private_ip && <code>{vm.private_ip}</code> },
                  { label: 'Public IP', value: vm.public_ip && <code>{vm.public_ip}</code> },
                  {
                    label: 'Private DNS',
                    value: vm.private_dns_name && <code>{vm.private_dns_name}</code>,
                  },
                  { label: 'VPC', value: vm.vpc_id && <code>{vm.vpc_id}</code> },
                  { label: 'Subnet', value: vm.subnet_id && <code>{vm.subnet_id}</code> },
                ]}
              >
                <NicsTable nics={vm.nics} />
                <SecurityGroupsTable sgs={vm.security_groups} />
              </NetworkingCard>

              <SectionTitle>Cloud identity</SectionTitle>
              <dl className="kv-list">
                <KV
                  k="Instance type"
                  v={vm.instance_type && <code>{vm.instance_type}</code>}
                />
                <KV k="Architecture" v={vm.architecture} />
                <KV k="Zone" v={vm.zone && <code>{vm.zone}</code>} />
                <KV k="Region" v={vm.region && <code>{vm.region}</code>} />
                <KV k="Image" v={vm.image_name} />
                <KV k="Image ID" v={vm.image_id && <code>{vm.image_id}</code>} />
                <KV k="Keypair" v={vm.keypair_name && <code>{vm.keypair_name}</code>} />
                <KV k="Boot mode" v={vm.boot_mode} />
                <KV
                  k="Provider account"
                  v={vm.provider_account_id && <code>{vm.provider_account_id}</code>}
                />
                <KV
                  k="Created at provider"
                  v={vm.provider_creation_date && formatTs(vm.provider_creation_date)}
                />
              </dl>

              <SectionTitle>Power</SectionTitle>
              <dl className="kv-list">
                <KV k="Power state" v={<PowerStatePill state={vm.power_state} />} />
                <KV k="Ready" v={vm.ready ? 'yes' : 'no'} />
                <KV k="Deletion protection" v={vm.deletion_protection ? 'enabled' : 'disabled'} />
                <KV k="State reason" v={vm.state_reason} />
              </dl>

              <SectionTitle>OS stack</SectionTitle>
              <dl className="kv-list">
                <KV
                  k="Kernel"
                  v={vm.kernel_version ? <code>{vm.kernel_version}</code> : <Dash />}
                />
                <KV
                  k="Operating system"
                  v={vm.operating_system ? vm.operating_system : <Dash />}
                />
              </dl>

              <CapacityCard
                showAllocatable={false}
                rows={[
                  { dimension: 'CPU', capacity: vm.capacity_cpu },
                  { dimension: 'Memory', capacity: vm.capacity_memory },
                ]}
                emptyMessage={
                  vm.instance_type
                    ? `Unknown (instance type ${vm.instance_type} not in capacity table)`
                    : 'Unknown'
                }
              />

              <SectionTitle>Storage</SectionTitle>
              <dl className="kv-list">
                <KV
                  k="Root device type"
                  v={vm.root_device_type && <code>{vm.root_device_type}</code>}
                />
                <KV
                  k="Root device name"
                  v={vm.root_device_name && <code>{vm.root_device_name}</code>}
                />
              </dl>
              <BlockDevicesTable devices={vm.block_devices} />

              <ApplicationsCard
                applications={vm.applications}
                onSave={(apps) =>
                  api
                    .updateVirtualMachine(vm.id, { applications: apps })
                    .then(() => undefined)
                }
                onSaved={reload}
              />

              <LabelsCard labels={vm.tags} title="Tags (provider)" />
              <LabelsCard labels={vm.labels} title="Labels" />
              <AnnotationsCard annotations={vm.annotations} />

              <SectionTitle>Lifecycle</SectionTitle>
              <dl className="kv-list">
                <KV k="Created" v={formatTs(vm.created_at)} />
                <KV k="Updated" v={formatTs(vm.updated_at)} />
                <KV k="Last seen" v={formatTs(vm.last_seen_at)} />
                <KV
                  k="Terminated"
                  v={
                    vm.terminated_at ? (
                      <span className="vm-terminated-ts">{formatTs(vm.terminated_at)}</span>
                    ) : undefined
                  }
                />
              </dl>
            </>
          );
        }}
      </AsyncView>
    </>
  );
}

function NicsTable({ nics }: { nics?: api.VMNic[] | null }) {
  if (!nics?.length) {
    return (
      <details className="vm-subsection" open={false}>
        <summary>NICs</summary>
        <Empty message="No NICs reported by provider." />
      </details>
    );
  }
  return (
    <details className="vm-subsection" open>
      <summary>
        NICs <span className="muted">({nics.length})</span>
      </summary>
      <table className="entities">
        <thead>
          <tr>
            <th>Idx</th>
            <th>MAC</th>
            <th>Private IP</th>
            <th>Public IP</th>
            <th>Subnet</th>
            <th>Security groups</th>
          </tr>
        </thead>
        <tbody>
          {nics.map((n, i) => (
            <tr key={n.id ?? i}>
              <td>{n.device_index ?? i}</td>
              <td>{n.mac_address ? <code>{n.mac_address}</code> : <Dash />}</td>
              <td>{n.private_ip ? <code>{n.private_ip}</code> : <Dash />}</td>
              <td>{n.public_ip ? <code>{n.public_ip}</code> : <Dash />}</td>
              <td>{n.subnet_id ? <code>{n.subnet_id}</code> : <Dash />}</td>
              <td>
                {n.security_group_ids?.length ? (
                  <code>{n.security_group_ids.join(', ')}</code>
                ) : (
                  <Dash />
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </details>
  );
}

function SecurityGroupsTable({ sgs }: { sgs?: api.VMSecurityGroup[] | null }) {
  if (!sgs?.length) return null;
  return (
    <details className="vm-subsection" open>
      <summary>
        Security groups <span className="muted">({sgs.length})</span>
      </summary>
      <table className="entities">
        <thead>
          <tr>
            <th>ID</th>
            <th>Name</th>
          </tr>
        </thead>
        <tbody>
          {sgs.map((s, i) => (
            <tr key={s.id ?? i}>
              <td>{s.id ? <code>{s.id}</code> : <Dash />}</td>
              <td>{s.name ?? <Dash />}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </details>
  );
}

function BlockDevicesTable({ devices }: { devices?: api.VMBlockDevice[] | null }) {
  if (!devices?.length) {
    return (
      <details className="vm-subsection" open={false}>
        <summary>Block devices</summary>
        <Empty message="No block devices reported by provider." />
      </details>
    );
  }
  return (
    <details className="vm-subsection" open>
      <summary>
        Block devices <span className="muted">({devices.length})</span>
      </summary>
      <table className="entities">
        <thead>
          <tr>
            <th>Device</th>
            <th>Volume</th>
            <th>Type</th>
            <th>Size (GiB)</th>
            <th>IOPS</th>
            <th>Delete on terminate</th>
          </tr>
        </thead>
        <tbody>
          {devices.map((d, i) => (
            <tr key={d.volume_id ?? i}>
              <td>{d.device_name ? <code>{d.device_name}</code> : <Dash />}</td>
              <td>{d.volume_id ? <code>{d.volume_id}</code> : <Dash />}</td>
              <td>{d.volume_type ?? <Dash />}</td>
              <td>{d.size_gib ?? <Dash />}</td>
              <td>{d.iops ?? <Dash />}</td>
              <td>{d.delete_on_termination ? 'yes' : 'no'}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </details>
  );
}
