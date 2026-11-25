// RouteSeats draws the Seats section of round 8's page D: a read-only table with one row per seat of the route registry the
// page holds, in the order the registry holds them. Each row names the seat's id, its auth mode, its upstream, the model it
// sends and notes that say what the seat does with the caller's login, account, or API key. A seat is neither added nor
// edited here.

import type { VNode } from "preact";

import type { RouteRegistry, RouteSeat } from "./gen/orrery/v1/accounts_pb";

export interface RouteSeatsProps {
  // registry is the route registry the page holds, whose seats are the rows, in the order the registry holds them.
  registry: RouteRegistry;
}

// upstreamOf is the upstream a seat shows. A sigv4 seat has none, so it shows its AWS region.
function upstreamOf(seat: RouteSeat): string {
  return seat.authMode === "sigv4" ? `region ${seat.awsRegion}` : seat.upstream;
}

// modelOf is the model a seat shows: as asked when the seat names none, so the caller's own model goes through.
function modelOf(seat: RouteSeat): string {
  return seat.model === "" ? "as asked" : seat.model;
}

// notesOf is what a seat's notes say, by its auth mode. A forward seat sends the context's account, or the session's login;
// a sigv4 seat maps models and signs with its profile; and an inject seat, the only mode left, says its billing, and its
// speed when it has one.
function notesOf(seat: RouteSeat): string {
  if (seat.authMode === "forward") {
    return "the context's account, else the session's login";
  }
  if (seat.authMode === "sigv4") {
    return `model map · profile ${seat.awsProfile}`;
  }
  return seat.speed === "" ? seat.billing : `${seat.billing} · ${seat.speed}`;
}

export function RouteSeats({ registry }: RouteSeatsProps): VNode {
  return (
    <div class="seat-wrap">
      <table class="seat-table">
        <thead>
          <tr>
            <th>seat</th>
            <th>kind</th>
            <th>upstream</th>
            <th>model</th>
            <th>notes</th>
          </tr>
        </thead>
        <tbody>
          {registry.seats.map((seat) => (
            <tr key={seat.id} data-seat={seat.id}>
              <td class="main">{seat.id}</td>
              <td>{seat.authMode}</td>
              <td class="code">{upstreamOf(seat)}</td>
              <td>{modelOf(seat)}</td>
              <td>{notesOf(seat)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
