# Copyright 2025-2026 Daniel Seufferth
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""database_endpoint package: the runner's common database endpoint contract.

The dispatcher classifies a database host as a DNS name or an IPv4/IPv6
literal, resolves DNS hosts to an ordered, de-duplicated address list, and
dials candidate addresses in resolver order under a shared 10-second deadline;
the first successful connection owns the operation. This mirrors the Go
contracts shipped by the Experiment Operator probe and the Translator
template's internal/databaseendpoint dispatcher so all three clients classify,
order, de-duplicate, time-bound, and fall back identically.
"""

RESOLUTION_DEADLINE_SECONDS = 10.0
