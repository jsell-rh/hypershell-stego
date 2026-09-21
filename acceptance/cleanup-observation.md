# Gateway cleanup observations

`scripts/collect-gateway-cleanup.py` reads one bounded snapshot during an
existing browser workflow. It is a test observation tool. It does not change
the application or its cleanup order.

Use the explicit saved operator context and the browser fixture namespace.
Write each sample to a new file in the persistent workflow result directory:

```sh
python3 -B scripts/collect-gateway-cleanup.py \
  --context="$operator_context" --fixture-namespace=stego-service-ci \
  --output="$result_directory/cleanup-pods-001.json"
```

Only namespaces with that fixture's allocator label are selected. Each list
has a limit of 16 items and one response page. A request has a 5-second server
timeout and a maximum 7-second process timeout. A whole sample has a 30-second
deadline. The script reads no Secrets and makes no cluster changes.

The record keeps namespace and Pod UIDs, deletion timestamps, finalizers,
condition codes, Pod owner references, grace periods, and container exit
states. It excludes annotations, container commands, environment variables,
application messages, and command error output. Namespace UIDs are checked
again after Pod reads. A changed namespace selection, excess data, or failed
read produces an incomplete record with no partial resource snapshot.

Use a bounded operator observer to sample this command during the same
Gateway deletion that produces the API cleanup timing record. Bind its source
revision, run ID, and test Job UID in that observer's plan. Do not start a new
workflow only to obtain a sample. Retain incomplete samples as gaps. Limit the
sampling duration and record count to the existing test deadline.

These are sequential observations. Resource state can change between reads.
Pod owners are recorded, not independently proved by this collector. Label
selection and an empty Pod list do not prove resource deletion. The separate
workflow and operator cleanup checks remain required. Live use and timing
analysis remain pending; the unit checks do not prove the cause of delay or
the 30-second cleanup target.
